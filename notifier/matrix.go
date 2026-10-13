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

// SendMatrix pushes alert message to Matrix chat via webhook.
// Retries the whole send path (login → join → media → text) on failure.
func SendMatrix(event models.Event, snapshot io.Reader, provider notifMeta) {
	profile := config.ConfigData.Alerts.Matrix[provider.index]
	status := &config.Internal.Status.Notifications.Matrix[provider.index]

	// Build notification
	var message string
	if profile.Template != "" {
		message = renderMessage(profile.Template, event, "message", "Matrix")
	} else {
		message = renderMessage("html", event, "message", "Matrix")
	}

	// Pre-read media into memory once, so retry can reuse the bytes.
	var clipBytes, snapshotBytes []byte

<<<<<<< Updated upstream
	// Ignore self-signed certs if set
	if profile.Insecure {
		httpClient := &http.Client{Timeout: 10 * time.Second}
		// Ignore SSL verification if set
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
		m.Client = httpClient
	}

	// Handle login
	m.StateStore = mautrix.NewMemoryStateStore()
	ch, err := cryptohelper.NewCryptoHelper(m, []byte("asdf"), "./matrix.db")
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
	ch.LoginAs = &mautrix.ReqLogin{
		Type:       mautrix.AuthTypePassword,
		Identifier: mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: profile.Username},
		Password:   profile.Password,
	}
	ch.Init(context.Background())
	m.Crypto = ch

	// Join room if needed
	_, err = m.JoinRoomByID(context.Background(), id.RoomID(profile.RoomID))
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

	// Send snapshot image if available
	if event.HasSnapshot {
		var media *mautrix.RespMediaUpload
		img, _ := io.ReadAll(snapshot)
		media, _ = m.UploadMedia(context.Background(), mautrix.ReqUploadMedia{ContentBytes: img, ContentType: "image/jpeg", FileName: "snapshot.jpg"})
		_, err = m.SendMessageEvent(context.Background(), id.RoomID(profile.RoomID), evt.EventMessage, &evt.MessageEventContent{
			MsgType: evt.MsgImage,
			Body:    "snapshot.jpg",
			URL:     media.ContentURI.CUString(),
			Info:    &evt.FileInfo{MimeType: "image/jpeg"},
		})
		if err != nil {
=======
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
			}
		}
	}

	if len(clipBytes) == 0 && event.HasSnapshot {
		var readErr error
		snapshotBytes, readErr = io.ReadAll(snapshot)
		if readErr != nil {
>>>>>>> Stashed changes
			log.Warn().
				Str("event_id", event.ID).
				Str("provider", "Matrix").
				Int("provider_id", provider.index).
<<<<<<< Updated upstream
				Err(err).
				Msg("Unable to send alert")
			status.NotifFailure(err.Error())
			return
=======
				Err(readErr).
				Msg("Unable to read snapshot, sending text only")
			snapshotBytes = nil
>>>>>>> Stashed changes
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
			Msg("Unable to send alert after retries")
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
// login → join → media (clip или snapshot) → text.
// Возвращает ошибку на первом же сбое, чтобы retrySend мог повторить.
func trySendMatrixEvent(profile models.Matrix, message string, clipBytes, snapshotBytes []byte) error {
	m, err := mautrix.NewClient(profile.Server, "", "")
	if err != nil {
		return err
	}

	// Ignore self-signed certs if set
	if profile.Insecure {
		m.Client = &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
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

	// Send media: prefer clip, fallback to snapshot
	if len(clipBytes) > 0 {
		if err := sendMatrixMedia(m, profile, clipBytes, "video/mp4", "clip.mp4", evt.MsgVideo); err != nil {
			return err
		}
	} else if len(snapshotBytes) > 0 {
		if err := sendMatrixMedia(m, profile, snapshotBytes, "image/jpeg", "snapshot.jpg", evt.MsgImage); err != nil {
			return err
		}
	}

	// Send event details
	_, err = m.SendMessageEvent(context.Background(),
		id.RoomID(profile.RoomID), evt.EventMessage,
		&evt.MessageEventContent{
			MsgType:       evt.MsgText,
			Format:        "org.matrix.custom.html",
			FormattedBody: message,
		})
	return err
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
