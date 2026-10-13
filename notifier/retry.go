package notifier

import (
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// RetryConfig управляет поведением retrySend.
type RetryConfig struct {
	MaxAttempts int           // Общее число попыток (включая первую)
	BaseDelay   time.Duration // Базовая пауза между попытками
	Backoff     float64       // Множитель паузы (2.0 = удвоение)
	MaxDelay    time.Duration // Потолок паузы
}

// DefaultRetry — разумные значения по умолчанию.
var DefaultRetry = RetryConfig{
	MaxAttempts: 3,
	BaseDelay:   5 * time.Second,
	Backoff:     2.0,
	MaxDelay:    60 * time.Second,
}

// retrySend вызывает send до MaxAttempts раз с экспоненциальной паузой.
// Возвращает последнюю ошибку, если все попытки провалились.
//
// `provider` и `providerID` используются только для логирования.
//
// Особый случай: если ошибка похожа на HTTP 429 (rate limit),
// пауза перед следующей попыткой умножается, чтобы дать
// удалённому серверу передышку.
func retrySend(provider string, providerID int, cfg RetryConfig, send func() error) error {
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}

	var lastErr error
	delay := cfg.BaseDelay

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		err := send()
		if err == nil {
			if attempt > 1 {
				log.Info().
					Str("provider", provider).
					Int("provider_id", providerID).
					Int("attempt", attempt).
					Msg("Send succeeded after retry")
			}
			return nil
		}

		lastErr = err
		log.Warn().
			Str("provider", provider).
			Int("provider_id", providerID).
			Int("attempt", attempt).
			Int("max_attempts", cfg.MaxAttempts).
			Err(err).
			Msg("Send attempt failed")

		if attempt == cfg.MaxAttempts {
			break
		}

		// Базовая пауза, ограниченная MaxDelay
		sleep := delay
		if sleep > cfg.MaxDelay {
			sleep = cfg.MaxDelay
		}

		// Для 429 (rate limit) — увеличиваем паузу, чтобы дать серверу передышку.
		// Matrix, Synapse и большинство API при 429 явно просят «подождать».
		// Множитель подбираем так, чтобы при BaseDelay=5s пауза стала ~20s.
		if isRateLimited(err) {
			sleep = sleep * 4
			if sleep > cfg.MaxDelay {
				sleep = cfg.MaxDelay
			}
			log.Debug().
				Str("provider", provider).
				Int("provider_id", providerID).
				Dur("sleep", sleep).
				Msg("Rate limited, extending pause before retry")
		}

		time.Sleep(sleep)
		delay = time.Duration(float64(delay) * cfg.Backoff)
	}

	return lastErr
}

// isRateLimited определяет, похожа ли ошибка на HTTP 429 / Too Many Requests.
func isRateLimited(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "m_limit_exceeded") ||
		strings.Contains(msg, "429")
}
