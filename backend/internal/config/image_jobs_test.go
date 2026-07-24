package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultImageJobsConfig(t *testing.T) {
	cfg := DefaultImageJobsConfig()
	require.False(t, cfg.Enabled)
	require.Equal(t, 2, cfg.WorkerConcurrency)
	require.Equal(t, 500, cfg.PollIntervalMilliseconds)
	require.Equal(t, 10, cfg.HeartbeatIntervalSeconds)
	require.Equal(t, 1800, cfg.TaskTimeoutSeconds)
	require.Equal(t, 4, cfg.MaxOutputsPerJob)
	require.Equal(t, 4, cfg.MaxInputImages)
	require.Equal(t, 86400, cfg.ResultTTLSeconds)
	require.Equal(t, 1.0, cfg.MaxReservationUSD)
	require.Equal(t, "s3", cfg.Storage.Driver)
	require.Equal(t, "auto", cfg.Storage.Region)
	require.Equal(t, "image-jobs/", cfg.Storage.Prefix)
}

func TestImageJobsConfigValidate(t *testing.T) {
	cfg := validImageJobsConfig()
	require.NoError(t, cfg.Validate())

	cfg.MaxInputImages = 17
	require.EqualError(t, cfg.Validate(), "gateway.image_jobs.max_input_images must be between 1 and 16")
}

func TestImageJobsConfigValidateRules(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ImageJobsConfig)
		wantErr string
	}{
		{
			name:    "worker concurrency must be positive",
			mutate:  func(cfg *ImageJobsConfig) { cfg.WorkerConcurrency = 0 },
			wantErr: "gateway.image_jobs.worker_concurrency must be positive",
		},
		{
			name:    "poll interval must be positive",
			mutate:  func(cfg *ImageJobsConfig) { cfg.PollIntervalMilliseconds = 0 },
			wantErr: "gateway.image_jobs.poll_interval_milliseconds must be positive",
		},
		{
			name:    "heartbeat interval must be positive",
			mutate:  func(cfg *ImageJobsConfig) { cfg.HeartbeatIntervalSeconds = 0 },
			wantErr: "gateway.image_jobs.heartbeat_interval_seconds must be positive",
		},
		{
			name:    "task timeout must be positive",
			mutate:  func(cfg *ImageJobsConfig) { cfg.TaskTimeoutSeconds = 0 },
			wantErr: "gateway.image_jobs.task_timeout_seconds must be positive",
		},
		{
			name: "heartbeat interval must be shorter than task timeout",
			mutate: func(cfg *ImageJobsConfig) {
				cfg.HeartbeatIntervalSeconds = cfg.TaskTimeoutSeconds
			},
			wantErr: "gateway.image_jobs.heartbeat_interval_seconds must be less than task_timeout_seconds",
		},
		{
			name:    "output count lower bound",
			mutate:  func(cfg *ImageJobsConfig) { cfg.MaxOutputsPerJob = 0 },
			wantErr: "gateway.image_jobs.max_outputs_per_job must be between 1 and 4",
		},
		{
			name:    "output count upper bound",
			mutate:  func(cfg *ImageJobsConfig) { cfg.MaxOutputsPerJob = 5 },
			wantErr: "gateway.image_jobs.max_outputs_per_job must be between 1 and 4",
		},
		{
			name:    "input count lower bound",
			mutate:  func(cfg *ImageJobsConfig) { cfg.MaxInputImages = 0 },
			wantErr: "gateway.image_jobs.max_input_images must be between 1 and 16",
		},
		{
			name:    "input count upper bound",
			mutate:  func(cfg *ImageJobsConfig) { cfg.MaxInputImages = 17 },
			wantErr: "gateway.image_jobs.max_input_images must be between 1 and 16",
		},
		{
			name:    "result ttl must be positive",
			mutate:  func(cfg *ImageJobsConfig) { cfg.ResultTTLSeconds = 0 },
			wantErr: "gateway.image_jobs.result_ttl_seconds must be positive",
		},
		{
			name:    "reservation must be positive",
			mutate:  func(cfg *ImageJobsConfig) { cfg.MaxReservationUSD = 0 },
			wantErr: "gateway.image_jobs.max_reservation_usd must be positive",
		},
		{
			name:    "s3 bucket is required",
			mutate:  func(cfg *ImageJobsConfig) { cfg.Storage.Bucket = "" },
			wantErr: "gateway.image_jobs.storage.bucket is required when storage.driver=s3",
		},
		{
			name:    "s3 access key is required",
			mutate:  func(cfg *ImageJobsConfig) { cfg.Storage.AccessKeyID = "" },
			wantErr: "gateway.image_jobs.storage.access_key_id is required when storage.driver=s3",
		},
		{
			name:    "s3 secret is required",
			mutate:  func(cfg *ImageJobsConfig) { cfg.Storage.SecretAccessKey = "" },
			wantErr: "gateway.image_jobs.storage.secret_access_key is required when storage.driver=s3",
		},
		{
			name: "local directory must be absolute",
			mutate: func(cfg *ImageJobsConfig) {
				cfg.Storage.Driver = "local"
				cfg.Storage.LocalDirectory = "image-jobs"
			},
			wantErr: "gateway.image_jobs.storage.local_directory must be an absolute path when storage.driver=local",
		},
		{
			name:    "storage driver must be supported",
			mutate:  func(cfg *ImageJobsConfig) { cfg.Storage.Driver = "memory" },
			wantErr: "gateway.image_jobs.storage.driver must be one of: s3/local",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validImageJobsConfig()
			tt.mutate(&cfg)
			require.EqualError(t, cfg.Validate(), tt.wantErr)
		})
	}

	disabled := DefaultImageJobsConfig()
	require.NoError(t, disabled.Validate())

	local := validImageJobsConfig()
	local.Storage.Driver = "local"
	local.Storage.LocalDirectory = "/var/lib/sub2api/image-jobs"
	require.NoError(t, local.Validate())
}

func TestConfigValidateCallsImageJobsValidate(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)

	cfg.Gateway.ImageJobs.MaxInputImages = 17
	require.EqualError(t, cfg.Validate(), "gateway.image_jobs.max_input_images must be between 1 and 16")
}

func TestLoadImageJobsConfigFromEnvironment(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("GATEWAY_IMAGE_JOBS_ENABLED", "true")
	t.Setenv("GATEWAY_IMAGE_JOBS_WORKER_CONCURRENCY", "3")
	t.Setenv("GATEWAY_IMAGE_JOBS_POLL_INTERVAL_MILLISECONDS", "750")
	t.Setenv("GATEWAY_IMAGE_JOBS_HEARTBEAT_INTERVAL_SECONDS", "20")
	t.Setenv("GATEWAY_IMAGE_JOBS_TASK_TIMEOUT_SECONDS", "1900")
	t.Setenv("GATEWAY_IMAGE_JOBS_MAX_OUTPUTS_PER_JOB", "3")
	t.Setenv("GATEWAY_IMAGE_JOBS_MAX_INPUT_IMAGES", "8")
	t.Setenv("GATEWAY_IMAGE_JOBS_RESULT_TTL_SECONDS", "7200")
	t.Setenv("GATEWAY_IMAGE_JOBS_MAX_RESERVATION_USD", "2.5")
	t.Setenv("GATEWAY_IMAGE_JOBS_STORAGE_DRIVER", "local")
	t.Setenv("GATEWAY_IMAGE_JOBS_STORAGE_ENDPOINT", "http://minio:9000")
	t.Setenv("GATEWAY_IMAGE_JOBS_STORAGE_REGION", "test-region")
	t.Setenv("GATEWAY_IMAGE_JOBS_STORAGE_BUCKET", "images")
	t.Setenv("GATEWAY_IMAGE_JOBS_STORAGE_PREFIX", "custom-prefix/")
	t.Setenv("GATEWAY_IMAGE_JOBS_STORAGE_ACCESS_KEY_ID", "key")
	t.Setenv("GATEWAY_IMAGE_JOBS_STORAGE_SECRET_ACCESS_KEY", "secret")
	t.Setenv("GATEWAY_IMAGE_JOBS_STORAGE_FORCE_PATH_STYLE", "true")
	t.Setenv("GATEWAY_IMAGE_JOBS_STORAGE_LOCAL_DIRECTORY", "/var/lib/sub2api/image-jobs")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, ImageJobsConfig{
		Enabled:                  true,
		WorkerConcurrency:        3,
		PollIntervalMilliseconds: 750,
		HeartbeatIntervalSeconds: 20,
		TaskTimeoutSeconds:       1900,
		MaxOutputsPerJob:         3,
		MaxInputImages:           8,
		ResultTTLSeconds:         7200,
		MaxReservationUSD:        2.5,
		Storage: ImageJobStorageConfig{
			Driver:          "local",
			Endpoint:        "http://minio:9000",
			Region:          "test-region",
			Bucket:          "images",
			Prefix:          "custom-prefix/",
			AccessKeyID:     "key",
			SecretAccessKey: "secret",
			ForcePathStyle:  true,
			LocalDirectory:  "/var/lib/sub2api/image-jobs",
		},
	}, cfg.Gateway.ImageJobs)
}

func validImageJobsConfig() ImageJobsConfig {
	cfg := DefaultImageJobsConfig()
	cfg.Enabled = true
	cfg.Storage.Bucket = "images"
	cfg.Storage.AccessKeyID = "key"
	cfg.Storage.SecretAccessKey = "secret"
	return cfg
}
