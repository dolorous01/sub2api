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
	defer os.Remove(dataTemp)

	metadataPath := path + ".meta.json"
	metadataTemp, err := stageAdjacentFile(metadataPath, metadata)
	if err != nil {
		return fmt.Errorf("stage image job object metadata: %w", err)
	}
	defer os.Remove(metadataTemp)

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
	defer os.Remove(tempPath)
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync image job object store health check: %w", err)
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
			os.Remove(tempPath)
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
	defer os.Remove(tempPath)
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
	defer file.Close()
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
	defer file.Close()
	return file.Sync()
}

func (s *localImageJobObjectStore) PutReader(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if reader == nil || size <= 0 || size > maxCanvasMediaObjectBytes {
		return fmt.Errorf("canvas media object size is invalid")
	}
	path, err := s.objectPath(key, true)
	if err != nil {
		return err
	}
	dataTemp, file, err := newAdjacentTemp(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return fmt.Errorf("create canvas media staging file: %w", err)
	}
	defer os.Remove(dataTemp)
	written, copyErr := io.Copy(file, io.LimitReader(&contextObjectReader{ctx: ctx, reader: reader}, size+1))
	if copyErr != nil || written != size {
		return errors.Join(fmt.Errorf("stage canvas media object: size mismatch"), copyErr, file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync canvas media object: %w", err), file.Close())
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close canvas media object: %w", err)
	}

	metadataPath := path + ".meta.json"
	metadata, err := json.Marshal(localImageJobObjectMetadata{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("marshal canvas media metadata: %w", err)
	}
	metadataTemp, err := stageAdjacentFile(metadataPath, metadata)
	if err != nil {
		return fmt.Errorf("stage canvas media metadata: %w", err)
	}
	defer os.Remove(metadataTemp)
	previousMetadata, metadataExisted, err := readPreviousMetadata(metadataPath)
	if err != nil {
		return fmt.Errorf("read previous canvas media metadata: %w", err)
	}
	if err := os.Rename(metadataTemp, metadataPath); err != nil {
		return fmt.Errorf("commit canvas media metadata: %w", err)
	}
	metadataTemp = ""
	if err := os.Rename(dataTemp, path); err != nil {
		commitErr := fmt.Errorf("commit canvas media object: %w", err)
		if rollbackErr := restoreMetadata(metadataPath, previousMetadata, metadataExisted); rollbackErr != nil {
			return errors.Join(commitErr, fmt.Errorf("restore canvas media metadata: %w", rollbackErr))
		}
		return commitErr
	}
	dataTemp = ""
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync canvas media object parent: %w", err)
	}
	return nil
}

func (s *localImageJobObjectStore) Open(ctx context.Context, key string) (io.ReadCloser, string, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", 0, err
	}
	path, err := s.objectPath(key, false)
	if err != nil {
		return nil, "", 0, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, "", 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxCanvasMediaObjectBytes {
		return nil, "", 0, fmt.Errorf("canvas media object is invalid")
	}
	metadata, err := readRegularFile(path+".meta.json", maxImageJobObjectMetadataBytes)
	if err != nil {
		return nil, "", 0, fmt.Errorf("read canvas media metadata: %w", err)
	}
	var meta localImageJobObjectMetadata
	if err := json.Unmarshal(metadata, &meta); err != nil {
		return nil, "", 0, fmt.Errorf("unmarshal canvas media metadata: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", 0, err
	}
	return file, meta.ContentType, info.Size(), nil
}

type contextObjectReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextObjectReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
