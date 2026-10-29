package notify

import (
	"context"
	"time"

	"github.com/fynhq/windwatch/internal/model"
	"github.com/fynhq/windwatch/internal/store"
)

// Config holds settings shared by every delivery. Channel-specific settings,
// including the notification language, live on model.NotificationChannel.
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
