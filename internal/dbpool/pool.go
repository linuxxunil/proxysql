package dbpool

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"proxysql/internal/config"
)

func New(ctx context.Context, c *config.Config) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(c.DB.DSN)
	if err != nil {
		return nil, err
	}
	if c.DB.PoolMax > 0 {
		pc.MaxConns = c.DB.PoolMax
	}
	if c.DB.PoolMin > 0 {
		pc.MinConns = c.DB.PoolMin
	}
	if c.DB.MaxLifetime.Duration > 0 {
		pc.MaxConnLifetime = c.DB.MaxLifetime.Duration
	}
	if c.DB.IdleTimeout.Duration > 0 {
		pc.MaxConnIdleTime = c.DB.IdleTimeout.Duration
	}
	if c.DB.HealthCheckPeriod.Duration > 0 {
		pc.HealthCheckPeriod = c.DB.HealthCheckPeriod.Duration
	}
	if c.DB.AcquireTimeout.Duration > 0 {
		pc.ConnConfig.ConnectTimeout = c.DB.AcquireTimeout.Duration
	}
	ctx, cancel := context.WithTimeout(ctx, timeoutOr(c.DB.AcquireTimeout.Duration, 10*time.Second))
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func timeoutOr(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}
