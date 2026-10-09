package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server Server `yaml:"server"`
	DB     DB     `yaml:"db"`
	Query  Query  `yaml:"query"`
	Auth   Auth   `yaml:"auth"`
	Log    Log    `yaml:"log"`
}

type Server struct {
	Listen       string `yaml:"listen"`
	BasePath     string `yaml:"base_path"`
	ReadTimeout  Dur    `yaml:"read_timeout"`
	WriteTimeout Dur    `yaml:"write_timeout"`
	MaxBodyBytes int64  `yaml:"max_body_bytes"`
}

type DB struct {
	// DSN lives in env only (PROXYSQL_DB__DSN): it carries the password and
	// must not appear in config.yaml. See validate().
	DSN               string `yaml:"-"`
	PoolMin           int32  `yaml:"pool_min"`
	PoolMax           int32  `yaml:"pool_max"`
	AcquireTimeout    Dur    `yaml:"acquire_timeout"`
	IdleTimeout       Dur    `yaml:"idle_timeout"`
	MaxLifetime       Dur    `yaml:"max_lifetime"`
	HealthCheckPeriod Dur    `yaml:"health_check_period"`
}

type Query struct {
	DefaultTimeout       Dur      `yaml:"default_timeout"`
	MaxTimeout           Dur      `yaml:"max_timeout"`
	MaxRows              int      `yaml:"max_rows"`
	AllowRawSQL          bool     `yaml:"allow_raw_sql"`
	AllowMultiStatements bool     `yaml:"allow_multi_statements"`
	AllowedStatements    []string `yaml:"allowed_statements"`
}

type Auth struct {
	APIKeys []string `yaml:"api_keys"`
}

type Log struct {
	Level      string `yaml:"level"`
	LogSQLText bool   `yaml:"log_sql_text"`
}

type Dur struct{ time.Duration }

func (d *Dur) UnmarshalYAML(v *yaml.Node) error {
	var s string
	if err := v.Decode(&s); err != nil {
		// allow numeric seconds
		var f float64
		if err2 := v.Decode(&f); err2 != nil {
			return err
		}
		d.Duration = time.Duration(f * float64(time.Second))
		return nil
	}
	du, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = du
	return nil
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	applyDefaults(&c)
	applyEnv(&c)
	if err := validate(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

func applyDefaults(c *Config) {
	if c.Server.Listen == "" {
		c.Server.Listen = ":8080"
	}
	if c.Server.BasePath == "" {
		c.Server.BasePath = "/"
	}
	if c.Server.MaxBodyBytes == 0 {
		c.Server.MaxBodyBytes = 1 << 20
	}
	if c.Query.MaxRows == 0 {
		c.Query.MaxRows = 10000
	}
	if c.Query.DefaultTimeout.Duration == 0 {
		c.Query.DefaultTimeout.Duration = 60 * time.Second
	}
	if c.Query.MaxTimeout.Duration == 0 {
		c.Query.MaxTimeout.Duration = 120 * time.Second
	}
	if len(c.Query.AllowedStatements) == 0 {
		c.Query.AllowedStatements = []string{"SELECT", "INSERT", "UPDATE", "CREATE"}
	}
}

func applyEnv(c *Config) {
	// DSN is env-only (single home). Empty = missing = startup failure.
	if v, ok := os.LookupEnv("PROXYSQL_DB__DSN"); ok {
		c.DB.DSN = strings.TrimSpace(v)
	}
}

// RedactedDSN returns the DSN with the password masked for safe logging.
func RedactedDSN(dsn string) string {
	at := strings.LastIndex(dsn, "@")
	if at < 0 {
		return dsn
	}
	scheme := strings.Index(dsn, "://")
	if scheme < 0 || scheme > at {
		return dsn
	}
	cred := dsn[scheme+3 : at]
	colon := strings.Index(cred, ":")
	if colon < 0 {
		return dsn
	}
	return dsn[:scheme+3] + cred[:colon] + ":***" + dsn[at:]
}

func validate(c *Config) error {
	if strings.TrimSpace(c.DB.DSN) == "" {
		return fmt.Errorf("db DSN is required via PROXYSQL_DB__DSN env (it is not read from config.yaml)")
	}
	if c.Query.DefaultTimeout.Duration > c.Query.MaxTimeout.Duration {
		return fmt.Errorf("query.default_timeout must be <= query.max_timeout")
	}
	return nil
}
