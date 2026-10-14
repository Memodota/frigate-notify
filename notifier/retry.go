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

// DefaultRetry — для уведомлений о событиях и summary.
// Скорость доставки не критична, важна надёжность:
// длинные паузы, много попыток.
var DefaultRetry = RetryConfig{
	MaxAttempts: 6,
	BaseDelay:   30 * time.Second,
	Backoff:     2.0,
	MaxDelay:    10 * time.Minute,
}

// retrySend вызывает send до MaxAttempts раз с экспоненциальной паузой.
// Возвращает последнюю ошибку, если все попытки провалились.
//
// `provider` и `providerID` используются только для логирования.
//
// Особые случаи:
//   - 401/403/unauthorized — НЕ повторяем, это постоянная ошибка.
//   - 429 (rate limit) — повторяем с увеличенной паузой.
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

		// Постоянные ошибки — не тратим попытки впустую
		if !isRetryable(err) {
			log.Warn().
				Str("provider", provider).
				Int("provider_id", providerID).
				Int("attempt", attempt).
				Err(err).
				Msg("Send failed with non-retryable error, giving up")
			return err
		}

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

		// Для 429 — увеличиваем паузу, чтобы дать серверу передышку.
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

// isRetryable решает, стоит ли повторять отправку при данной ошибке.
// Возвращает false для постоянных ошибок авторизации и клиентских 4xx,
// которые от повтора не изменятся.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())

	// Постоянные ошибки авторизации — retry бессмыслен
	if strings.Contains(msg, "missing_token") ||
		strings.Contains(msg, "unknown_token") ||
		strings.Contains(msg, "forbidden") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "m_forbidden") ||
		strings.Contains(msg, "m_unknown_token") {
		return false
	}

	// Всё остальное — считаем временным и пробуем снова:
	// сетевые сбои, таймауты, 429, 5xx и прочее.
	return true
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
