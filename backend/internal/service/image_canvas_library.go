package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxImageCanvasLibraryClientIDBytes = 128
	maxImageCanvasLibraryTitleRunes    = 240
	maxImageCanvasLibraryContentBytes  = 1 << 20
	maxImageCanvasLibraryTags          = 50
	maxImageCanvasLibraryTagRunes      = 64
	maxImageCanvasLibrarySourceRunes   = 240
	maxImageCanvasLibraryNoteBytes     = 16 << 10
	maxImageCanvasLibraryMetadataBytes = 64 << 10
)

func (s *ImageCanvasProjectService) ListLibraryItems(ctx context.Context, userID int64) ([]ImageCanvasLibraryItem, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("image canvas project repository is required")
	}
	if userID <= 0 {
		return nil, ErrImageCanvasLibraryItemInvalid
	}
	items, err := s.repository.ListLibraryItems(ctx, userID)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []ImageCanvasLibraryItem{}
	}
	return items, nil
}

func (s *ImageCanvasProjectService) CreateLibraryItem(
	ctx context.Context,
	userID int64,
	input ImageCanvasLibraryItemWrite,
) (*ImageCanvasLibraryItem, bool, error) {
	if s == nil || s.repository == nil {
		return nil, false, fmt.Errorf("image canvas project repository is required")
	}
	input.UserID = userID
	if err := s.validateLibraryItemWrite(ctx, &input, true); err != nil {
		return nil, false, err
	}
	return s.repository.CreateLibraryItem(ctx, input)
}

func (s *ImageCanvasProjectService) UpdateLibraryItem(
	ctx context.Context,
	userID int64,
	publicID string,
	input ImageCanvasLibraryItemWrite,
) (*ImageCanvasLibraryItem, error) {
	if s == nil || s.repository == nil {
		return nil, fmt.Errorf("image canvas project repository is required")
	}
	input.UserID = userID
	input.PublicID = strings.TrimSpace(publicID)
	if input.PublicID == "" || input.Version < 1 {
		return nil, ErrImageCanvasLibraryItemInvalid
	}
	if err := s.validateLibraryItemWrite(ctx, &input, false); err != nil {
		return nil, err
	}
	return s.repository.UpdateLibraryItem(ctx, input)
}

func (s *ImageCanvasProjectService) DeleteLibraryItem(ctx context.Context, userID int64, publicID string) error {
	if s == nil || s.repository == nil {
		return fmt.Errorf("image canvas project repository is required")
	}
	if userID <= 0 || strings.TrimSpace(publicID) == "" {
		return ErrImageCanvasLibraryItemInvalid
	}
	return s.repository.DeleteLibraryItem(ctx, userID, strings.TrimSpace(publicID))
}

func (s *ImageCanvasProjectService) validateLibraryItemWrite(
	ctx context.Context,
	input *ImageCanvasLibraryItemWrite,
	requireClientID bool,
) error {
	if input == nil || input.UserID <= 0 {
		return ErrImageCanvasLibraryItemInvalid
	}
	input.ClientID = strings.TrimSpace(input.ClientID)
	input.Kind = strings.ToLower(strings.TrimSpace(input.Kind))
	input.AssetPublicID = strings.TrimSpace(input.AssetPublicID)
	input.Title = strings.TrimSpace(input.Title)
	input.Source = strings.TrimSpace(input.Source)
	input.Note = strings.TrimSpace(input.Note)
	if (requireClientID && input.ClientID == "") || len(input.ClientID) > maxImageCanvasLibraryClientIDBytes || !utf8.ValidString(input.ClientID) {
		return fmt.Errorf("%w: client_id is invalid", ErrImageCanvasLibraryItemInvalid)
	}
	if input.Kind != "text" && input.Kind != "image" && input.Kind != "video" && input.Kind != "audio" {
		return fmt.Errorf("%w: kind is invalid", ErrImageCanvasLibraryItemInvalid)
	}
	if input.Title == "" || utf8.RuneCountInString(input.Title) > maxImageCanvasLibraryTitleRunes || !utf8.ValidString(input.Title) {
		return fmt.Errorf("%w: title is invalid", ErrImageCanvasLibraryItemInvalid)
	}
	if len(input.Content) > maxImageCanvasLibraryContentBytes || !utf8.ValidString(input.Content) {
		return fmt.Errorf("%w: content is invalid", ErrImageCanvasLibraryItemInvalid)
	}
	if input.Kind == "text" {
		if strings.TrimSpace(input.Content) == "" || input.AssetPublicID != "" {
			return fmt.Errorf("%w: text items require content and cannot reference an asset", ErrImageCanvasLibraryItemInvalid)
		}
	} else {
		input.Content = ""
		if input.AssetPublicID == "" {
			return fmt.Errorf("%w: media items require an asset", ErrImageCanvasLibraryItemInvalid)
		}
		asset, err := s.repository.GetAsset(ctx, input.UserID, input.AssetPublicID)
		if err != nil {
			return err
		}
		if asset.MediaKind != input.Kind {
			return fmt.Errorf("%w: asset media kind does not match item kind", ErrImageCanvasLibraryItemInvalid)
		}
	}
	if utf8.RuneCountInString(input.Source) > maxImageCanvasLibrarySourceRunes || len(input.Note) > maxImageCanvasLibraryNoteBytes || !utf8.ValidString(input.Note) {
		return fmt.Errorf("%w: source or note is invalid", ErrImageCanvasLibraryItemInvalid)
	}
	tags, err := normalizeImageCanvasLibraryTags(input.Tags)
	if err != nil {
		return err
	}
	input.Tags = tags
	if len(input.Metadata) == 0 {
		input.Metadata = json.RawMessage(`{}`)
	}
	if len(input.Metadata) > maxImageCanvasLibraryMetadataBytes || !json.Valid(input.Metadata) {
		return fmt.Errorf("%w: metadata is invalid", ErrImageCanvasLibraryItemInvalid)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(input.Metadata, &metadata); err != nil || metadata == nil {
		return fmt.Errorf("%w: metadata must be an object", ErrImageCanvasLibraryItemInvalid)
	}
	return nil
}

func normalizeImageCanvasLibraryTags(tags []string) ([]string, error) {
	if len(tags) > maxImageCanvasLibraryTags {
		return nil, fmt.Errorf("%w: too many tags", ErrImageCanvasLibraryItemInvalid)
	}
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, value := range tags {
		tag := strings.TrimSpace(value)
		if tag == "" {
			continue
		}
		if !utf8.ValidString(tag) || utf8.RuneCountInString(tag) > maxImageCanvasLibraryTagRunes {
			return nil, fmt.Errorf("%w: tag is invalid", ErrImageCanvasLibraryItemInvalid)
		}
		key := strings.ToLower(tag)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, tag)
	}
	return normalized, nil
}
