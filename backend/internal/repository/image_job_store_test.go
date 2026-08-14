package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestLocalImageJobObjectStoreRoundTrip(t *testing.T) {
	t.Parallel()

	store, err := NewLocalImageJobObjectStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalImageJobObjectStore() error = %v", err)
	}

	ctx := context.Background()
	key := "image-jobs/key/job/results/0.png"
	wantData := []byte("png payload")
	if err := store.Put(ctx, key, wantData, "image/png"); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	got, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !bytes.Equal(got.Data, wantData) {
		t.Fatalf("Get().Data = %q, want %q", got.Data, wantData)
	}
	if got.ContentType != "image/png" {
		t.Fatalf("Get().ContentType = %q, want %q", got.ContentType, "image/png")
	}
	if got.Size != int64(len(wantData)) {
		t.Fatalf("Get().Size = %d, want %d", got.Size, len(wantData))
	}
}

func TestValidateImageJobObjectKey(t *testing.T) {
	invalid := []string{
		"",
		"/image-jobs/key",
		"image-jobs\\key",
		".",
		"..",
		"./image-jobs/key",
		"image-jobs/./key",
		"image-jobs/../key",
		"image-jobs/key/..",
		"image-jobs/key\x00.png",
	}
	for _, key := range invalid {
		if err := validateImageJobObjectKey(key); err == nil {
			t.Errorf("validateImageJobObjectKey(%q) error = nil, want rejection", key)
		}
	}

	const valid = "image-jobs/key/job/results/0.png"
	if err := validateImageJobObjectKey(valid); err != nil {
		t.Fatalf("validateImageJobObjectKey(%q) error = %v", valid, err)
	}
}

func TestLocalImageJobObjectStoreRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalImageJobObjectStore(root)
	if err != nil {
		t.Fatalf("NewLocalImageJobObjectStore() error = %v", err)
	}

	key := "../escape"
	ctx := context.Background()
	if err := store.Put(ctx, key, []byte("escape"), "image/png"); err == nil {
		t.Error("Put() error = nil, want rejection")
	}
	if _, err := store.Get(ctx, key); err == nil {
		t.Error("Get() error = nil, want rejection")
	}
	if err := store.Delete(ctx, key); err == nil {
		t.Error("Delete() error = nil, want rejection")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("escape path stat error = %v, want not exist", err)
	}
}

func TestLocalImageJobObjectStoreRejectsSymlinkParentOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	store, err := NewLocalImageJobObjectStore(root)
	if err != nil {
		t.Fatalf("NewLocalImageJobObjectStore() error = %v", err)
	}
	if err := store.Put(context.Background(), "linked/object.png", []byte("payload"), "image/png"); err == nil {
		t.Error("Put() error = nil, want rejection for symlink parent")
	}
	if _, err := os.Stat(filepath.Join(outside, "object.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside object stat error = %v, want not exist", err)
	}
}

func TestLocalImageJobObjectStoreOverwriteAndDelete(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalImageJobObjectStore(root)
	if err != nil {
		t.Fatalf("NewLocalImageJobObjectStore() error = %v", err)
	}

	ctx := context.Background()
	key := "image-jobs/key/job/results/0.png"
	if err := store.Put(ctx, key, []byte("old"), "image/png"); err != nil {
		t.Fatalf("first Put() error = %v", err)
	}
	if err := store.Put(ctx, key, []byte("newest"), "image/webp"); err != nil {
		t.Fatalf("second Put() error = %v", err)
	}

	got, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !bytes.Equal(got.Data, []byte("newest")) || got.ContentType != "image/webp" || got.Size != 6 {
		t.Fatalf("Get() = %#v, want newest image/webp object", got)
	}

	dataPath := filepath.Join(root, filepath.FromSlash(key))
	metaPath := dataPath + ".meta.json"
	for _, path := range []string{dataPath, metaPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", path, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%q permissions = %#o, want 0600", path, info.Mode().Perm())
		}
	}

	metadata, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("ReadFile(metadata) error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(metadata, &decoded); err != nil {
		t.Fatalf("metadata JSON error = %v", err)
	}
	if decoded["content_type"] != "image/webp" {
		t.Errorf("metadata content_type = %v, want image/webp", decoded["content_type"])
	}

	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("temporary file remains: %q", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir() error = %v", err)
	}

	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("first Delete() error = %v", err)
	}
	for _, path := range []string{dataPath, metaPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Stat(%q) error = %v, want not exist", path, err)
		}
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("second Delete() error = %v, want nil", err)
	}
}

func TestLocalImageJobObjectStoreMetadataCommitFailurePreservesData(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalImageJobObjectStore(root)
	if err != nil {
		t.Fatalf("NewLocalImageJobObjectStore() error = %v", err)
	}

	ctx := context.Background()
	key := "image-jobs/key/job/results/0.png"
	if err := store.Put(ctx, key, []byte("old"), "image/png"); err != nil {
		t.Fatalf("first Put() error = %v", err)
	}

	dataPath := filepath.Join(root, filepath.FromSlash(key))
	metaPath := dataPath + ".meta.json"
	if err := os.Remove(metaPath); err != nil {
		t.Fatalf("Remove(metadata) error = %v", err)
	}
	if err := os.Mkdir(metaPath, 0o700); err != nil {
		t.Fatalf("Mkdir(metadata) error = %v", err)
	}

	if err := store.Put(ctx, key, []byte("new"), "image/webp"); err == nil {
		t.Error("second Put() error = nil, want metadata commit failure")
	}
	data, err := os.ReadFile(dataPath)
	if err != nil {
		t.Fatalf("ReadFile(data) error = %v", err)
	}
	if !bytes.Equal(data, []byte("old")) {
		t.Errorf("data = %q, want %q", data, "old")
	}
}

func TestLocalImageJobObjectStoreHealthAndAbsoluteRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "relative-root")
	store, err := NewLocalImageJobObjectStore(root)
	if err != nil {
		t.Fatalf("NewLocalImageJobObjectStore() error = %v", err)
	}
	if !filepath.IsAbs(store.root) {
		t.Errorf("store.root = %q, want absolute path", store.root)
	}
	if err := store.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	entries, err := os.ReadDir(store.root)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("Health() left %d files, want none", len(entries))
	}
}

func TestLocalImageJobObjectStoreGetRejectsOversizedSparseFile(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalImageJobObjectStore(root)
	if err != nil {
		t.Fatalf("NewLocalImageJobObjectStore() error = %v", err)
	}
	key := "image-jobs/key/job/results/large.png"
	path := filepath.Join(root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if err := file.Truncate(maxImageJobObjectBytes + 1); err != nil {
		file.Close()
		t.Fatalf("Truncate() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := store.Get(context.Background(), key); err == nil {
		t.Error("Get() error = nil, want oversized object rejection")
	}
}

func TestS3ImageJobObjectStorePut(t *testing.T) {
	client := &fakeImageS3Client{}
	store := &s3ImageJobObjectStore{client: client, bucket: "object-bucket", prefix: "image-jobs"}
	data := []byte("payload")
	if err := store.Put(context.Background(), "key/job/results/0.png", data, "image/png"); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if got := aws.ToString(client.putInput.Bucket); got != "object-bucket" {
		t.Errorf("PutObject bucket = %q, want object-bucket", got)
	}
	if got := aws.ToString(client.putInput.Key); got != "image-jobs/key/job/results/0.png" {
		t.Errorf("PutObject key = %q, want normalized prefix", got)
	}
	if got := aws.ToString(client.putInput.ContentType); got != "image/png" {
		t.Errorf("PutObject content type = %q, want image/png", got)
	}
	if got := aws.ToInt64(client.putInput.ContentLength); got != int64(len(data)) {
		t.Errorf("PutObject content length = %d, want %d", got, len(data))
	}
	body, err := io.ReadAll(client.putInput.Body)
	if err != nil {
		t.Fatalf("ReadAll(PutObject body) error = %v", err)
	}
	if !bytes.Equal(body, data) {
		t.Errorf("PutObject body = %q, want %q", body, data)
	}
}

func TestS3ImageJobObjectStoreGet(t *testing.T) {
	client := &fakeImageS3Client{getOutput: &s3.GetObjectOutput{
		Body:          io.NopCloser(bytes.NewReader([]byte("payload"))),
		ContentType:   aws.String("image/png"),
		ContentLength: aws.Int64(7),
	}}
	store := &s3ImageJobObjectStore{client: client, bucket: "object-bucket", prefix: "image-jobs"}

	got, err := store.Get(context.Background(), "key/job/results/0.png")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !bytes.Equal(got.Data, []byte("payload")) || got.ContentType != "image/png" || got.Size != 7 {
		t.Fatalf("Get() = %#v, want payload image/png object", got)
	}
	if got := aws.ToString(client.getInput.Bucket); got != "object-bucket" {
		t.Errorf("GetObject bucket = %q, want object-bucket", got)
	}
	if got := aws.ToString(client.getInput.Key); got != "image-jobs/key/job/results/0.png" {
		t.Errorf("GetObject key = %q, want normalized prefix", got)
	}
}

func TestS3ImageJobObjectStoreGetRejectsOversizedContentLength(t *testing.T) {
	client := &fakeImageS3Client{getOutput: &s3.GetObjectOutput{
		Body:          io.NopCloser(bytes.NewReader(nil)),
		ContentLength: aws.Int64(maxImageJobObjectBytes + 1),
	}}
	store := &s3ImageJobObjectStore{client: client, bucket: "object-bucket"}
	if _, err := store.Get(context.Background(), "key"); err == nil {
		t.Error("Get() error = nil, want oversized content length rejection")
	}
}

func TestS3ImageJobObjectStoreGetRejectsOversizedStream(t *testing.T) {
	client := &fakeImageS3Client{getOutput: &s3.GetObjectOutput{
		Body:          io.NopCloser(io.LimitReader(zeroReader{}, maxImageJobObjectBytes+1)),
		ContentLength: aws.Int64(-1),
	}}
	store := &s3ImageJobObjectStore{client: client, bucket: "object-bucket"}
	if _, err := store.Get(context.Background(), "key"); err == nil {
		t.Error("Get() error = nil, want oversized stream rejection")
	}
}

func TestS3ImageJobObjectStoreDelete(t *testing.T) {
	missing := &smithy.GenericAPIError{Code: "NoSuchKey", Message: "missing"}
	store := &s3ImageJobObjectStore{client: &fakeImageS3Client{deleteErr: missing}, bucket: "object-bucket"}
	if err := store.Delete(context.Background(), "key"); err != nil {
		t.Fatalf("Delete() missing object error = %v, want nil", err)
	}

	want := errors.New("permission denied")
	store.client = &fakeImageS3Client{deleteErr: want}
	if err := store.Delete(context.Background(), "key"); !errors.Is(err, want) {
		t.Errorf("Delete() error = %v, want %v", err, want)
	}
}

func TestS3ImageJobObjectStoreHealth(t *testing.T) {
	client := &fakeImageS3Client{}
	store := &s3ImageJobObjectStore{client: client, bucket: "object-bucket"}
	if err := store.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if got := aws.ToString(client.headInput.Bucket); got != "object-bucket" {
		t.Errorf("HeadBucket bucket = %q, want object-bucket", got)
	}
}

func TestNewImageJobObjectStoreNormalizesDriver(t *testing.T) {
	local, err := NewImageJobObjectStore(config.ImageJobStorageConfig{
		Driver:         " LOCAL ",
		LocalDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewImageJobObjectStore(LOCAL) error = %v", err)
	}
	if _, ok := local.(*localImageJobObjectStore); !ok {
		t.Errorf("NewImageJobObjectStore(LOCAL) = %T, want *localImageJobObjectStore", local)
	}

	if _, err := NewImageJobObjectStore(config.ImageJobStorageConfig{
		Driver:          " S3 ",
		Region:          "auto",
		Bucket:          "bucket",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
	}); err != nil {
		t.Fatalf("NewImageJobObjectStore(S3) error = %v", err)
	}
	if _, err := NewImageJobObjectStore(config.ImageJobStorageConfig{Driver: "filesystem"}); err == nil {
		t.Error("NewImageJobObjectStore(unsupported) error = nil, want rejection")
	}
}

func TestProvideImageJobObjectStoreDoesNotInitializeStorageWhenDisabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.ImageJobs.Enabled = false
	cfg.Gateway.ImageJobs.Storage.Driver = "s3"

	store, err := ProvideImageJobObjectStore(cfg)
	if err != nil {
		t.Fatalf("ProvideImageJobObjectStore() error = %v", err)
	}
	if _, ok := store.(*disabledImageJobObjectStore); !ok {
		t.Fatalf("ProvideImageJobObjectStore() = %T, want disabled store", store)
	}
}

type fakeImageS3Client struct {
	putInput    *s3.PutObjectInput
	getInput    *s3.GetObjectInput
	getOutput   *s3.GetObjectOutput
	getErr      error
	deleteInput *s3.DeleteObjectInput
	deleteErr   error
	headInput   *s3.HeadBucketInput
	headErr     error
}

func (f *fakeImageS3Client) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.putInput = input
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeImageS3Client) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.getInput = input
	return f.getOutput, f.getErr
}

func (f *fakeImageS3Client) DeleteObject(_ context.Context, input *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	f.deleteInput = input
	return &s3.DeleteObjectOutput{}, f.deleteErr
}

func (f *fakeImageS3Client) HeadBucket(_ context.Context, input *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	f.headInput = input
	return &s3.HeadBucketOutput{}, f.headErr
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
