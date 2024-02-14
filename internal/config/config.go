package config

import (
	"fmt"
	"slices"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

// Config is the global configuration for application.
// It reads values from environment variables or a yaml config file
type Config struct {
	// The environment of application.
	// Defaults to `production`.
	// Possible values: `production`, `development`, `test`.
	Env string `default:"production" yaml:"env"`
	// The logging level.
	// Defaults to `info`.
	// Possible values: `debug`, `info`, `warning`, `error`, `panic`, `fatal`.
	LogLevel string `default:"info" split_words:"true" yaml:"log_level"`

	Kube KubeConfing `yaml:"kube"` // K8S related settings.
	GRPC GRPCConfig  `yaml:"grpc"` // gRPC service related settings.
	HTTP HTTPConfig  `yaml:"http"` // HTTP service related settings.

	Lease LeaseConfig `yaml:"lease"` // Lease related settings.

	// Driver indicates the lease driver, currently only supports `redis` driver.
	// Defaults to `redis`.
	Driver string `default:"redis" yaml:"driver"`
	// Redis related settings, it only take effects when the driver is `redis`.
	Redis RedisConfig `yaml:"redis"`
}

// Kubernetes related settings
type KubeConfing struct {
	Enable bool `default:"true" yaml:"enable"` // Whether to enable k8s service. Defaults to `true`.
	// InCluster indicates if the application is in or outside the k8s cluster. Defaults to `true`.
	InCluster bool `default:"true" split_words:"true" yaml:"in_cluster"`
}

// GRPC related settings
type GRPCConfig struct {
	Enable bool `default:"true" yaml:"enable"` // Whether to enable gRPC service. Defaults to `true`.
	Port   int  `default:"443" yaml:"port"`    // Port is the gRPC service port. Defaults to `443`.
}

type HTTPConfig struct {
	Enable bool `default:"true" yaml:"enable"` // Whether to enable HTTP service. Defaults to `true`.
	Port   int  `default:"80" yaml:"port"`     // Port is the HTTP service port. Defaults to `80`.
}
type RedisConfig struct {
	// Mode indicates the redis server mode. Differenet modes require different URLs format.
	// Defaults to `single``.
	// Possible values: `single`,`cluster`,`failover`.
	Mode string `default:"single" yaml:"mode"`
	// Prefix specifies the redis key prefix, the lease key will be formatted as `[prefix]/[lease name]``.
	// Defaults to `ela`.
	Prefix string `default:"ela" yaml:"prefix"`

	// URLs is a list of redis servers.
	//
	// Single mode: the url format of each redis server is: `redis://<user>:<password>@<host>:<port>/<db_number>`.
	//
	// Failover mode: the url format of each redis server is: `redis://<user>:<password>@<host>:<port>/<db_number>`.
	//
	// Cluster mode: the url format of each redis server is: `redis://<user>:<password>@<host>:<port>?addr=<host2>:<port2>&addr=<host3>:<port3>`.
	URLs []string `yaml:"urls"`
	// Master is the redis master name, it only take effects when the 'mode' is "failover"(sentinel)
	Master string `yaml:"master"`
}

type LeaseConfig struct {
	// Enable lease caching or not.
	// Caching is useful when the number of leases is small but access frequency is high
	Cache bool `default:"false" split_words:"true" yaml:"cache"`
	// The size of caching pool. It only take effects when 'Cache' is true
	CacheSize int `default:"8192" split_words:"true" yaml:"cache_size"`
}

var Default *Config

func Init() error {
	// TODO: support reading config from YAML file

	_ = godotenv.Load()
	var cfg Config
	if err := envconfig.Process("EA", &cfg); err != nil {
		return err
	}

	// TODO: implement stricter sanity check
	if cfg.Driver == "redis" && !slices.Contains([]string{"single", "failover", "cluster"}, cfg.Redis.Mode) {
		return fmt.Errorf("Unsupported redis mode:%s", cfg.Redis.Mode)
	}
	Default = &cfg

	return nil
}

func GetDefault() *Config {
	return Default
}

func (cfg *Config) IsProdEnv() bool {
	return cfg.Env == "production"
}

func (cfg *Config) IsDevEnv() bool {
	return cfg.Env == "development"
}

func (cfg *Config) IsTestEnv() bool {
	return cfg.Env == "test"
}

func (cfg *Config) IsBenchmarkEnv() bool {
	return cfg.Env == "benchmark"
}
