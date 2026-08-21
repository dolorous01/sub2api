package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const maxImageJobObjectMetadataBytes int64 = 64 << 10

type localImageJobObjectStore struct {
	root string
}

type localImageJobObjectMetadata struct {
	ContentType string `json:"content_type"`
}

var _ service.ImageJobObjectStore = (*localImageJobObjectStore)(nil)

func NewLocalImageJobObjectStore(root string) (*localImageJobObjectStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("image job object store root is required")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve image job object store root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create image job object store root: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve image job object store root symlinks: %w", err)
	}
	info, err := os.Stat(realRoot)
	if err != nil {
		return nil, fmt.Errorf("stat image job object store root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("image job object store root is not a directory")
	}
	return &localImageJobObjectStore{root: realRoot}, nil
}

func (s *localImageJobObjectStore) Put(ctx context.Context, key string, data []byte, contentType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if int64(len(data)) > maxImageJobObjectBytes {
		return fmt.Errorf("image job object exceeds maximum size")
	}
	path, err := s.objectPath(key, true)
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(localImageJobObjectMetadata{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("marshal image job object metadata: %w", err)
	}
	dataTemp, err := stageAdjacentFile(path, data)
	if err != nil {
		return fmt.Errorf("stage image job object: %w", err)
	}
	defer func() {
		if dataTemp != "" {
			_ = os.Remove(dataTemp)
		}
	}()

	metadataPath := path + ".meta.json"
	metadataTemp, err := stageAdjacentFile(metadataPath, metadata)
	if err != nil {
		return fmt.Errorf("stage image job object metadata: %w", err)
	}
	defer func() {
		if metadataTemp != "" {
			_ = os.Remove(metadataTemp)
		}
	}()

	previousMetadata, metadataExisted, err := readPreviousMetadata(metadataPath)
	if err != nil {
		return fmt.Errorf("read previous image job object metadata: %w", err)
	}
	if err := os.Rename(metadataTemp, metadataPath); err != nil {
		return fmt.Errorf("commit image job object metadata: %w", err)
	}
	metadataTemp = ""
	if err := os.Rename(dataTemp, path); err != nil {
		dataCommitErr := fmt.Errorf("commit image job object: %w", err)
		if rollbackErr := restoreMetadata(metadataPath, previousMetadata, metadataExisted); rollbackErr != nil {
			return errors.Join(dataCommitErr, fmt.Errorf("restore previous image job object metadata: %w", rollbackErr))
		}
		return dataCommitErr
	}
	dataTemp = ""
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync image job object parent: %w", err)
	}
	return nil
}

func (s *localImageJobObjectStore) Get(ctx context.Context, key string) (*service.ImageJobObject, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := s.objectPath(key, false)
	if err != nil {
		return nil, err
	}
	data, err := readRegularFile(path, maxImageJobObjectBytes)
	if err != nil {
		return nil, fmt.Errorf("read image job object: %w", err)
	}
	metadata, err := readRegularFile(path+".meta.json", maxImageJobObjectMetadataBytes)
	if err != nil {
		return nil, fmt.Errorf("read image job object metadata: %w", err)
	}
	var meta localImageJobObjectMetadata
	if err := json.Unmarshal(metadata, &meta); err != nil {
		return nil, fmt.Errorf("unmarshal image job object metadata: %w", err)
	}
	return &service.ImageJobObject{Data: data, ContentType: meta.ContentType, Size: int64(len(data))}, nil
}

func (s *localImageJobObjectStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.objectPath(key, false)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var removeErrs []error
	for _, target := range []string{path, path + ".meta.json"} {
		if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
			removeErrs = append(removeErrs, fmt.Errorf("remove image job object %q: %w", target, err))
		}
	}
	return errors.Join(removeErrs...)
}

func (s *localImageJobObjectStore) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.ensureParents(s.root, false); err != nil {
		return err
	}
	tempPath, file, err := newAdjacentTemp(s.root, ".health")
	if err != nil {
		return fmt.Errorf("create image job object store health check: %w", err)
	}
	defer func() { _ = os.Remove(tempPath) }()
	if err := file.Sync(); err != nil {
		syncErr := fmt.Errorf("sync image job object store health check: %w", err)
		if closeErr := file.Close(); closeErr != nil {
			return errors.Join(syncErr, fmt.Errorf("close image job object store health check: %w", closeErr))
		}
		return syncErr
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close image job object store health check: %w", err)
	}
	if err := os.Remove(tempPath); err != nil {
		return fmt.Errorf("remove image job object store health check: %w", err)
	}
	if err := syncDirectory(s.root); err != nil {
		return fmt.Errorf("sync image job object store root: %w", err)
	}
	return nil
}

func (s *localImageJobObjectStore) objectPath(key string, createParents bool) (string, error) {
	if err := validateImageJobObjectKey(key); err != nil {
		return "", err
	}
	path := filepath.Join(s.root, filepath.FromSlash(key))
	rel, err := filepath.Rel(s.root, path)
	if err != nil {
		return "", fmt.Errorf("resolve image job object path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("image job object path escapes storage root")
	}
	if err := s.ensureParents(filepath.Dir(path), createParents); err != nil {
		return "", err
	}
	return path, nil
}

func (s *localImageJobObjectStore) ensureParents(directory string, create bool) error {
	rootInfo, err := os.Lstat(s.root)
	if err != nil {
		return fmt.Errorf("stat image job object store root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return fmt.Errorf("image job object store root is not a directory")
	}
	rel, err := filepath.Rel(s.root, directory)
	if err != nil {
		return fmt.Errorf("resolve image job object parent: %w", err)
	}
	if rel == "." {
		return nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("image job object parent escapes storage root")
	}
	current := s.root
	for _, segment := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			if !create {
				return err
			}
			if err := os.Mkdir(current, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("create image job object parent: %w", err)
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return fmt.Errorf("stat image job object parent: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("image job object parent is not a directory")
		}
	}
	return nil
}

func stageAdjacentFile(destination string, data []byte) (string, error) {
	tempPath, file, err := newAdjacentTemp(filepath.Dir(destination), filepath.Base(destination))
	if err != nil {
		return "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	if written, err := file.Write(data); err != nil {
		return "", errors.Join(err, file.Close())
	} else if written != len(data) {
		return "", errors.Join(io.ErrShortWrite, file.Close())
	}
	if err := file.Sync(); err != nil {
		return "", errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	cleanup = false
	return tempPath, nil
}

func readPreviousMetadata(path string) ([]byte, bool, error) {
	metadata, err := readRegularFile(path, maxImageJobObjectMetadataBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return metadata, true, nil
}

func restoreMetadata(path string, previous []byte, existed bool) error {
	if !existed {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}

	tempPath, err := stageAdjacentFile(path, previous)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tempPath) }()
	return os.Rename(tempPath, path)
}

func newAdjacentTemp(directory, name string) (string, *os.File, error) {
	for range 16 {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, err
		}
		path := filepath.Join(directory, "."+name+"."+hex.EncodeToString(random[:])+".tmp")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return path, file, err
	}
	return "", nil, fmt.Errorf("create unique temporary file")
}

func readRegularFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("file exceeds maximum size")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds maximum size")
	}
	return data, nil
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(syncErr, closeErr)
}
