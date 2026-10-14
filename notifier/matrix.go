package notifier

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog/log"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	evt "maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/0x2142/frigate-notify/config"
	"github.com/0x2142/frigate-notify/models"
)

// SendMatrix pushes alert message to Matrix chat via webhook
func SendMatrix(event models.Event, snapshot io.Reader, provider notifMeta) {
	profile := config.ConfigData.Alerts.Matrix[provider.index]
	status := &config.Internal.Status.Notifications.Matrix[provider.index]

	var err error
	var message string
	// Build notification
	if profile.Template != "" {
		message = renderMessage(profile.Template, event, "message", "Matrix")
	} else {
		message = renderMessage("html", event, "message", "Matrix")
	}

	// New matrix client
	m, err := mautrix.NewClient(profile.Server, "", "")
	if err != nil {
		log.Warn().
			Str("event_id", event.ID).
			Str("provider", "Matrix").
			Err(err).
			Int("provider_id", provider.index).
			Msg("Unable to send alert")
		status.NotifFailure(err.Error())
		return
	}

	// Snapshot — read first, primary artifact
	if event.HasSnapshot {
		var readErr error
		snapshotBytes, readErr = io.ReadAll(snapshot)
		if readErr != nil {
			log.Warn().
				Str("event_id", event.ID).
				Str("provider", "Matrix").
				Int("provider_id", provider.index).
				Err(readErr).
				Msg("Unable to read snapshot, sending text only")
			snapshotBytes = nil
		}
	}

	// Clip — read only if enabled and size fits within the limit.
	if event.HasClip && profile.SendClip {
		clip := GetClip(event)
		if clip == nil {
			log.Warn().
				Str("event_id", event.ID).
				Str("provider", "Matrix").
				Int("provider_id", provider.index).
				Msg("Clip not available, will fall back to snapshot")
		} else {
			var readErr error
			clipBytes, readErr = io.ReadAll(clip)
			if readErr != nil {
				log.Warn().
					Str("event_id", event.ID).
					Str("provider", "Matrix").
					Int("provider_id", provider.index).
					Err(readErr).
					Msg("Unable to read clip, will fall back to snapshot")
				clipBytes = nil
			} else if profile.MaxClipSizeMB > 0 && len(clipBytes) > profile.MaxClipSizeMB*1024*1024 {
				log.Info().
					Str("event_id", event.ID).
					Str("provider", "Matrix").
					Int("provider_id", provider.index).
					Int("size_bytes", len(clipBytes)).
					Int("limit_mb", profile.MaxClipSizeMB).
					Msg("Clip exceeds size limit, will fall back to snapshot")
				clipBytes = nil
			}
		}
	}

	// Send with retry
	err := retrySend("Matrix", provider.index, DefaultRetry, func() error {
		return trySendMatrixEvent(profile, message, clipBytes, snapshotBytes)
	})
	if err != nil {
		log.Warn().
			Str("event_id", event.ID).
			Str("provider", "Matrix").
			Int("provider_id", provider.index).
			Err(err).
			Msg("Unable to send alert")
		status.NotifFailure(err.Error())
		return
	}

	log.Info().
		Str("event_id", event.ID).
		Str("provider", "Matrix").
		Int("provider_id", provider.index).
		Msg("Alert sent")
	status.NotifSuccess()
}

// trySendMatrixEvent — одна попытка отправки события:
// login → join → snapshot → text → clip (if any).
// Возвращает ошибку на первом же сбое, КРОМЕ отправки клипа:
// если снапшот и текст уже ушли, клип больше не влияет на результат.
func trySendMatrixEvent(profile models.Matrix, message string, clipBytes, snapshotBytes []byte) error {
	m, err := mautrix.NewClient(profile.Server, "", "")
	if err != nil {
		return err
	}

	// Extended timeout for media uploads, self-signed certs if set
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

	// Handle login
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

	// Join room if needed
	if _, err := m.JoinRoomByID(context.Background(), id.RoomID(profile.RoomID)); err != nil {
		return err
	}

	// 1. Snapshot first — quickest, most reliable artifact
	if len(snapshotBytes) > 0 {
		if err := sendMatrixMedia(m, profile, snapshotBytes, "image/jpeg", "snapshot.jpg", evt.MsgImage); err != nil {
			return err
		}
	}

	// 2. Text — the main event details
	_, err = m.SendMessageEvent(context.Background(),
		id.RoomID(profile.RoomID), evt.EventMessage,
		&evt.MessageEventContent{
			MsgType:       evt.MsgText,
			Format:        "org.matrix.custom.html",
			FormattedBody: message,
		})
	if err != nil {
		return err
	}

	// 3. Clip last — failure here doesn't fail the whole attempt,
	// because snapshot+text already went through. Swallow the error.
	if len(clipBytes) > 0 {
		if err := sendMatrixMedia(m, profile, clipBytes, "video/mp4", "clip.mp4", evt.MsgVideo); err != nil {
			log.Warn().
				Str("provider", "Matrix").
				Int("provider_id", -1).
				Int("size_bytes", len(clipBytes)).
				Err(err).
				Msg("Unable to send clip, but snapshot and text were delivered")
		} else {
			log.Info().
				Str("provider", "Matrix").
				Int("size_bytes", len(clipBytes)).
				Msg("Clip sent")
		}
	}

	return nil
}

// sendMatrixMedia загружает байты в Matrix и отправляет их как сообщение.
func sendMatrixMedia(m *mautrix.Client, profile models.Matrix, data []byte, contentType, fileName string, msgType evt.MessageType) error {
	media, err := m.UploadMedia(context.Background(), mautrix.ReqUploadMedia{
		ContentBytes: data,
		ContentType:  contentType,
		FileName:     fileName,
	})
	if err != nil {
		return err
	}

	_, err = m.SendMessageEvent(context.Background(),
		id.RoomID(profile.RoomID), evt.EventMessage,
		&evt.MessageEventContent{
			MsgType: msgType,
			Body:    fileName,
			URL:     media.ContentURI.CUString(),
			Info: &evt.FileInfo{
				MimeType: contentType,
				Size:     len(data),
			},
		})
	return err
}
