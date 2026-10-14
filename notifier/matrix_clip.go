package notifier

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	evt "maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/0x2142/frigate-notify/models"
)

// ClipRetry — параметры retry для загрузки клипов.
// 5 попыток с паузами 2/4/8/10/10 минут (backoff 2.0, cap 10 min).
var ClipRetry = RetryConfig{
	MaxAttempts: 5,
	BaseDelay:   2 * time.Minute,
	Backoff:     2.0,
	MaxDelay:    10 * time.Minute,
}

// clipSem — семафор на 1 одновременную загрузку клипа.
// Защита от лавины: клипы идут строго по одному.
var clipSem = make(chan struct{}, 1)

// sendClipWithRetry — фоновая отправка клипа с retry.
// Не блокирует основной поток уведомлений.
func sendClipWithRetry(event models.Event, profile models.Matrix, idx int) {
	// Занимаем слот. Если другой клип уже грузится — ждём здесь.
	clipSem <- struct{}{}
	defer func() { <-clipSem }()

	clip := GetClip(event)
	if clip == nil {
		log.Debug().
			Str("event_id", event.ID).
			Msg("Clip not available")
		return
	}

	vid, readErr := io.ReadAll(clip)
	if readErr != nil {
		log.Warn().
			Str("event_id", event.ID).
			Err(readErr).
			Msg("Unable to read clip")
		return
	}

	limitBytes := profile.MaxClipSizeMB * 1024 * 1024
	if profile.MaxClipSizeMB > 0 && len(vid) > limitBytes {
		log.Info().
			Str("event_id", event.ID).
			Int("size_bytes", len(vid)).
			Int("limit_mb", profile.MaxClipSizeMB).
			Msg("Clip exceeds size limit, skipping upload")
		return
	}

	err := retrySend("Matrix-clip", idx, ClipRetry, func() error {
		return trySendClip(vid, event, profile)
	})
	if err != nil {
		log.Warn().
			Str("event_id", event.ID).
			Int("size_bytes", len(vid)).
			Err(err).
			Msg("Unable to send clip, but snapshot and text were delivered")
	} else {
		log.Info().
			Str("event_id", event.ID).
			Int("size_bytes", len(vid)).
			Msg("Clip sent")
	}
}

// trySendClip — одна попытка отправки клипа (создаёт свой Matrix-клиент).
func trySendClip(vid []byte, event models.Event, profile models.Matrix) error {
	m, err := mautrix.NewClient(profile.Server, "", "")
	if err != nil {
		return err
	}

	transport := &http.Transport{
		ResponseHeaderTimeout: 60 * time.Second,
	}
	if profile.Insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	m.Client = &http.Client{
		Timeout:   5 * time.Minute,
		Transport: transport,
	}

	m.StateStore = mautrix.NewMemoryStateStore()
	ch, err := cryptohelper.NewCryptoHelper(m, []byte("asdf"), "./matrix.db")
	if err != nil {
		return err
	}
	ch.LoginAs = &mautrix.ReqLogin{
		Type:       mautrix.AuthTypePassword,
		Identifier: mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: profile.Username},
		Password:   profile.Password,
	}
	if err := ch.Init(context.Background()); err != nil {
		return err
	}
	m.Crypto = ch

	if _, err := m.JoinRoomByID(context.Background(), id.RoomID(profile.RoomID)); err != nil {
		log.Warn().Err(err).Msg("clip: join room")
	}

	media, err := m.UploadMedia(context.Background(), mautrix.ReqUploadMedia{
		ContentBytes: vid,
		ContentType:  "video/mp4",
		FileName:     "clip.mp4",
	})
	if err != nil {
		return err
	}

	_, err = m.SendMessageEvent(context.Background(),
		id.RoomID(profile.RoomID), evt.EventMessage,
		&evt.MessageEventContent{
			MsgType: evt.MsgVideo,
			Body:    "clip.mp4",
			URL:     media.ContentURI.CUString(),
			Info: &evt.FileInfo{
				MimeType: "video/mp4",
				Size:     len(vid),
			},
		})
	return err
}
