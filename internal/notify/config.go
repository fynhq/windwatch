package notify

import (
	"context"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/store"
)

type Config struct {
	Timezone *time.Location
}

func LoadConfig(ctx context.Context, st *store.Store) (Config, error) {
	values, err := st.Settings(ctx)
	if err != nil {
		return Config{}, err
	}
	return Config{Timezone: model.TimezoneFromSettings(values)}, nil
}
