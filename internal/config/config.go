package config

import (
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	yaml "sigs.k8s.io/yaml/goyaml.v2"
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

	// The name of election agent. It's required when `Zone.Enable` is `true`
	Name string `yaml:"name"`

	// Prefix specifies the key prefix.
	// The lease key will be formatted as `[cfg.KeyPrefix]/lease/<lease name>`.
	// The agent info key will be formatted as `[cfg.KeyPrefix]/info/[cfg.Name]/<field name>`.
	// Defaults to `ela`.
	KeyPrefix string `default:"ela" split_words:"true" yaml:"key_prefix"`

	// The default state. Set this field in each agent for active-active or active-standby mode
	// Defaults to `active`
	// Possible values: `active`, `standby`, `unavailable`
	DefaultState string `default:"active" split_words:"true" yaml:"default_state"`
	// The state cache TTL, set it to zero for disabling state cache.
	// The state cache will be expired when zone health checker doesn't update state for `StateCacheTTL` duration.
	// Defaults to `30s`.
	StateCacheTTL time.Duration `default:"0s" split_words:"true" yaml:"state_cache_ttl"`

	Kube KubeConfig `yaml:"kube"` // K8S related settings.
	GRPC GRPCConfig `yaml:"grpc"` // gRPC service related settings.
	HTTP HTTPConfig `yaml:"http"` // HTTP service related settings.

	Lease LeaseConfig `yaml:"lease"` // Lease related settings.

	Zone ZoneConfig `yaml:"zone"` // K8S multi-zone related settings.

	// Driver indicates the lease driver, currently only supports `redis` driver.
	// Defaults to `redis`.
	Driver string `default:"redis" yaml:"driver"`
	// Redis related settings, it only take effects when the driver is `redis`.
	// Redis related settings, it only take effects when the driver is `redis`.
	Redis RedisConfig `yaml:"redis"`
}

func (cfg *Config) AgentInfoKey(field string) string {
	return cfg.KeyPrefix + "/info/" + cfg.Name + "/" + field
}

// Kubernetes related settings
type KubeConfig struct {
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

	// URLs is a list of redis servers.
	//
	// Single mode: the url format of each redis server is: `redis://<user>:<password>@<host>:<port>/<db_number>`.
	//
	// Failover mode: the url format of each redis server is: `redis://<user>:<password>@<host>:<port>/<db_number>`.
	//
	// Cluster mode: the url format of each redis server is: `redis://<user>:<password>@<host>:<port>?addr=<host2>:<port2>&addr=<host3>:<port3>`.
	URLs []string `yaml:"urls"`

	// Primary specifies which one is the primary Redis server in the "URLS" field.
	// The primary redis server is the only redis server used by the agent when it enters orphan(standalone) mode.
	// Defaults to `0`.
	Primary int `default:"0" yaml:"primary"`

	// Timeout of whole multiple redis node operations.
	// Defaults to `3s`
	OpearationTimeout time.Duration `default:"3s" split_words:"true" yaml:"operation_timeout"`
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

type ZoneConfig struct {
	// Whether to enable multi-zone feature.
	// It's the initial value when the agent starts, and it will be changed by API request.
	// Defaults to `false`.
	Enable bool `default:"false" yaml:"enable"`
	// The name of zone where the election agent resides.
	Name string `yaml:"name"`
	// StateKeyPrefix specifies the key prefix of state that election agent stores in the backend.
	// The full key name will be: `<Zone.StateKeyPrefix>/<Name>`
	// Defaults to `ela_state`.
	StateKeyPrefix string `default:"ela_state" split_words:"true" yaml:"state_key_prefix"`

	// The zone health check interval.
	// Defaults to `1s`.
	CheckInterval time.Duration `default:"1s" split_words:"true" yaml:"check_interval"`

	// The execution timeout of zone health check.
	// Defaults to `3s`.
	CheckTimeout time.Duration `default:"3s" split_words:"true" yaml:"check_timeout"`

	// The zone coordinator's url.
	CoordinatorURL string `split_words:"true" yaml:"coordinator_url"`
	// The request timeout of zone coordinator.
	// Defaults to `1s`.
	CoordinatorTimeout time.Duration `default:"1s" split_words:"true" yaml:"coordinator_timeout"`

	// A list of election agent peer gRPC URLs.
	PeerURLs []string `envconfig:"EA_ZONE_PEER_URLS" yaml:"peer_urls"`
	// The request timeout of peer.
	// Defaults to `1s`.
	PeerTimeout time.Duration `default:"1s" split_words:"true" yaml:"peer_timeout"`
}

var Default *Config

func loadYAMLConfig(cfg *Config) error {
	yamlFilePath := os.Getenv("EA_CONFIG_FILE")
	if yamlFilePath == "" {
		return nil
	}

	if _, err := os.Stat(yamlFilePath); err != nil {
		return fmt.Errorf("The config yaml file '%s' doesn't exist", yamlFilePath)
	}

	data, err := os.ReadFile(yamlFilePath)
	if err != nil {
		return fmt.Errorf("Failed to read config yaml file '%s', error:%w", yamlFilePath, err)
	}

	return yaml.Unmarshal(data, cfg)
}

func Init() error {
	// TODO: support reading config from YAML file

	_ = godotenv.Load()
	var cfg Config
	if err := envconfig.Process("EA", &cfg); err != nil {
		return err
	}

	// The settings in the config file will override environment variables
	if err := loadYAMLConfig(&cfg); err != nil {
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
