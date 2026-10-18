package service

import (
	sq "github.com/Masterminds/squirrel"
	"github.com/bochkarevart/1c-selection/internal/cache"
	"github.com/bochkarevart/1c-selection/internal/store"
)

var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

type Service struct {
	db    store.Database
	cache *cache.Cache
	cfg   Config
}

func New(db store.Database, c *cache.Cache, cfg Config) *Service {
	return &Service{
		db:    db,
		cache: c,
		cfg:   cfg,
	}
}
