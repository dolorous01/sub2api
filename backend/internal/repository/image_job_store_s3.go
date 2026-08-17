package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type s3CompatibleClientConfig struct {
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool
}

func newS3CompatibleClient(ctx context.Context, cfg s3CompatibleClientConfig) (*s3.Client, error) {
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = "auto"
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		if cfg.Endpoint != "" {
			options.BaseEndpoint = &cfg.Endpoint
		}
		if cfg.ForcePathStyle {
			options.UsePathStyle = true
		}
		options.APIOptions = append(options.APIOptions, v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware)
		options.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	}), nil
}

type imageJobS3API interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	HeadBucket(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
}

type s3ImageJobObjectStore struct {
	client imageJobS3API
	bucket string
	prefix string
}

var _ service.ImageJobObjectStore = (*s3ImageJobObjectStore)(nil)

func newS3ImageJobObjectStore(cfg config.ImageJobStorageConfig) (*s3ImageJobObjectStore, error) {
	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		return nil, fmt.Errorf("image job object store bucket is required")
	}
	client, err := newS3CompatibleClient(context.Background(), s3CompatibleClientConfig{
		Region:          cfg.Region,
		Endpoint:        cfg.Endpoint,
		AccessKeyID:     cfg.AccessKeyID,
		SecretAccessKey: cfg.SecretAccessKey,
		ForcePathStyle:  cfg.ForcePathStyle,
	})
	if err != nil {
		return nil, err
	}
	return &s3ImageJobObjectStore{client: client, bucket: bucket, prefix: strings.Trim(cfg.Prefix, "/")}, nil
}

func (s *s3ImageJobObjectStore) Put(ctx context.Context, key string, data []byte, contentType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if int64(len(data)) > maxImageJobObjectBytes {
		return fmt.Errorf("image job object exceeds maximum size")
	}
	objectKey, err := s.objectKey(key)
	if err != nil {
		return err
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(objectKey),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
		ContentType:   aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("put image job object: %w", err)
	}
	return nil
}

func (s *s3ImageJobObjectStore) Get(ctx context.Context, key string) (*service.ImageJobObject, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	objectKey, err := s.objectKey(key)
	if err != nil {
		return nil, err
	}
	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(objectKey)})
	if err != nil {
		return nil, fmt.Errorf("get image job object: %w", err)
	}
	if output == nil || output.Body == nil {
		return nil, fmt.Errorf("get image job object: empty response body")
	}
	defer output.Body.Close()
	if output.ContentLength != nil && *output.ContentLength > maxImageJobObjectBytes {
		return nil, fmt.Errorf("image job object exceeds maximum size")
	}
	data, err := io.ReadAll(io.LimitReader(output.Body, maxImageJobObjectBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read image job object: %w", err)
	}
	if int64(len(data)) > maxImageJobObjectBytes {
		return nil, fmt.Errorf("image job object exceeds maximum size")
	}
	return &service.ImageJobObject{Data: data, ContentType: aws.ToString(output.ContentType), Size: int64(len(data))}, nil
}

func (s *s3ImageJobObjectStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	objectKey, err := s.objectKey(key)
	if err != nil {
		return err
	}
	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(objectKey)})
	if err == nil || isS3ObjectNotFound(err) {
		return nil
	}
	return fmt.Errorf("delete image job object: %w", err)
}

func (s *s3ImageJobObjectStore) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	if err != nil {
		return fmt.Errorf("head image job object store bucket: %w", err)
	}
	return nil
}

func (s *s3ImageJobObjectStore) objectKey(key string) (string, error) {
	if err := validateImageJobObjectKey(key); err != nil {
		return "", err
	}
	prefix := strings.Trim(s.prefix, "/")
	if prefix == "" {
		return key, nil
	}
	return prefix + "/" + key, nil
}

func isS3ObjectNotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound", "404":
			return true
		}
	}
	var statusErr interface{ HTTPStatusCode() int }
	return errors.As(err, &statusErr) && statusErr.HTTPStatusCode() == 404
}
