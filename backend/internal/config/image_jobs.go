package config

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
)

type ImageJobsConfig struct {
	Enabled                  bool                  `mapstructure:"enabled"`
	WorkerConcurrency        int                   `mapstructure:"worker_concurrency"`
	PollIntervalMilliseconds int                   `mapstructure:"poll_interval_milliseconds"`
	HeartbeatIntervalSeconds int                   `mapstructure:"heartbeat_interval_seconds"`
	TaskTimeoutSeconds       int                   `mapstructure:"task_timeout_seconds"`
	MaxOutputsPerJob         int                   `mapstructure:"max_outputs_per_job"`
	MaxInputImages           int                   `mapstructure:"max_input_images"`
	ResultTTLSeconds         int                   `mapstructure:"result_ttl_seconds"`
	MaxReservationUSD        float64               `mapstructure:"max_reservation_usd"`
	Storage                  ImageJobStorageConfig `mapstructure:"storage"`
}

type ImageJobStorageConfig struct {
	Driver          string `mapstructure:"driver"`
	Endpoint        string `mapstructure:"endpoint"`
	Region          string `mapstructure:"region"`
	Bucket          string `mapstructure:"bucket"`
	Prefix          string `mapstructure:"prefix"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	SecretAccessKey string `mapstructure:"secret_access_key"`
	ForcePathStyle  bool   `mapstructure:"force_path_style"`
	LocalDirectory  string `mapstructure:"local_directory"`
}

func DefaultImageJobsConfig() ImageJobsConfig {
	return ImageJobsConfig{
		WorkerConcurrency:        2,
		PollIntervalMilliseconds: 500,
		HeartbeatIntervalSeconds: 10,
		TaskTimeoutSeconds:       1800,
		MaxOutputsPerJob:         4,
		MaxInputImages:           4,
		ResultTTLSeconds:         86400,
		MaxReservationUSD:        1,
		Storage: ImageJobStorageConfig{
			Driver: "s3",
			Region: "auto",
			Prefix: "image-jobs/",
		},
	}
}

func (c ImageJobsConfig) Validate() error {
	if c.WorkerConcurrency <= 0 {
		return fmt.Errorf("gateway.image_jobs.worker_concurrency must be positive")
	}
	if c.PollIntervalMilliseconds <= 0 {
		return fmt.Errorf("gateway.image_jobs.poll_interval_milliseconds must be positive")
	}
	if c.HeartbeatIntervalSeconds <= 0 {
		return fmt.Errorf("gateway.image_jobs.heartbeat_interval_seconds must be positive")
	}
	if c.TaskTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.image_jobs.task_timeout_seconds must be positive")
	}
	if c.HeartbeatIntervalSeconds >= c.TaskTimeoutSeconds {
		return fmt.Errorf("gateway.image_jobs.heartbeat_interval_seconds must be less than task_timeout_seconds")
	}
	if c.MaxOutputsPerJob < 1 || c.MaxOutputsPerJob > 4 {
		return fmt.Errorf("gateway.image_jobs.max_outputs_per_job must be between 1 and 4")
	}
	if c.MaxInputImages < 1 || c.MaxInputImages > 16 {
		return fmt.Errorf("gateway.image_jobs.max_input_images must be between 1 and 16")
	}
	if c.ResultTTLSeconds <= 0 {
		return fmt.Errorf("gateway.image_jobs.result_ttl_seconds must be positive")
	}
	if math.IsNaN(c.MaxReservationUSD) || math.IsInf(c.MaxReservationUSD, 0) {
		return fmt.Errorf("gateway.image_jobs.max_reservation_usd must be finite")
	}
	if c.MaxReservationUSD <= 0 {
		return fmt.Errorf("gateway.image_jobs.max_reservation_usd must be positive")
	}
	if !c.Enabled {
		return nil
	}

	switch c.Storage.Driver {
	case "s3":
		if strings.TrimSpace(c.Storage.Bucket) == "" {
			return fmt.Errorf("gateway.image_jobs.storage.bucket is required when storage.driver=s3")
		}
		if strings.TrimSpace(c.Storage.AccessKeyID) == "" {
			return fmt.Errorf("gateway.image_jobs.storage.access_key_id is required when storage.driver=s3")
		}
		if strings.TrimSpace(c.Storage.SecretAccessKey) == "" {
			return fmt.Errorf("gateway.image_jobs.storage.secret_access_key is required when storage.driver=s3")
		}
	case "local":
		if !filepath.IsAbs(c.Storage.LocalDirectory) {
			return fmt.Errorf("gateway.image_jobs.storage.local_directory must be an absolute path when storage.driver=local")
		}
	default:
		return fmt.Errorf("gateway.image_jobs.storage.driver must be one of: s3/local")
	}

	return nil
}
