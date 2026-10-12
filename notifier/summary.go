package notifier

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/0x2142/frigate-notify/config"
	"github.com/0x2142/frigate-notify/util"
)

// SummaryResponse — одна строка агрегированной сводки от Frigate.
// Формат ответа /api/events/summary.
type SummaryResponse struct {
	Day    string `json:"day"`
	Camera string `json:"camera"`
	Label  string `json:"label"`
	Count  int    `json:"count"`
}

// fetchSummary запрашивает /api/events/summary и фильтрует по дню.
func fetchSummary(daysBack int) ([]SummaryResponse, error) {
	if daysBack < 1 {
		daysBack = 1
	}

	url := fmt.Sprintf("%s/api/events/summary", config.ConfigData.Frigate.Server)

	// HTTPGet сам подтянет cookies авторизации, если URL начинается с Frigate.Server
	body, err := util.HTTPGet(
		url,
		config.ConfigData.Frigate.Insecure,
		"",
		config.ConfigData.Frigate.Headers...,
	)
	if err != nil {
		return nil, fmt.Errorf("summary: HTTPGet: %w", err)
	}

	var all []SummaryResponse
	if err := json.Unmarshal(body, &all); err != nil {
		return nil, fmt.Errorf("summary: unmarshal: %w", err)
	}

	target := time.Now().AddDate(0, 0, -daysBack).Format("2006-01-02")
	var filtered []SummaryResponse
	for _, s := range all {
		if s.Day == target {
			filtered = append(filtered, s)
		}
	}
	return filtered, nil
}

// buildSummaryMessage формирует читаемый текст.
func buildSummaryMessage(events []SummaryResponse, day string) string {
	if len(events) == 0 {
		return fmt.Sprintf("📊 Сводка Frigate за %s: событий не найдено.", day)
	}

	byCamera := make(map[string][]SummaryResponse)
	totalByLabel := make(map[string]int)
	total := 0
	for _, e := range events {
		byCamera[e.Camera] = append(byCamera[e.Camera], e)
		totalByLabel[e.Label] += e.Count
		total += e.Count
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📊 <b>Сводка Frigate за %s</b>\n", day))
	sb.WriteString(fmt.Sprintf("Всего событий: <b>%d</b>\n\n", total))

	cameras := make([]string, 0, len(byCamera))
	for c := range byCamera {
		cameras = append(cameras, c)
	}
	sort.Strings(cameras)

	for _, cam := range cameras {
		sb.WriteString(fmt.Sprintf("📹 <b>%s</b>\n", cam))
		rows := byCamera[cam]
		sort.Slice(rows, func(i, j int) bool { return rows[i].Count > rows[j].Count })
		for _, r := range rows {
			sb.WriteString(fmt.Sprintf("  • %s: %d\n", r.Label, r.Count))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Итого по типам:\n")
	labels := make([]string, 0, len(totalByLabel))
	for l := range totalByLabel {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	for _, l := range labels {
		sb.WriteString(fmt.Sprintf("  • %s: %d\n", l, totalByLabel[l]))
	}

	return sb.String()
}

// SendSummary собирает и рассылает ежедневную сводку.
func SendSummary() {
	days := config.ConfigData.Summary.Days
	if days < 1 {
		days = 1
	}
	targetDay := time.Now().AddDate(0, 0, -days).Format("2006-01-02")

	events, err := fetchSummary(days)
	if err != nil {
		log.Warn().Err(err).Msg("Не удалось получить сводку из Frigate")
		return
	}

	message := buildSummaryMessage(events, targetDay)
	log.Debug().Str("day", targetDay).Int("rows", len(events)).Msg("Summary built")

	// Отправка в Matrix (расширяйте на другие провайдеры по аналогии)
	for i := range config.ConfigData.Alerts.Matrix {
		if !config.ConfigData.Alerts.Matrix[i].Enabled {
			continue
		}
		if err := sendMatrixSummary(message, i); err != nil {
			log.Warn().Err(err).Int("provider_id", i).Msg("Matrix summary send failed")
		} else {
			log.Info().Int("provider_id", i).Msg("Matrix summary sent")
		}
	}
}
