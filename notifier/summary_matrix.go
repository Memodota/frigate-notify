package notifier

import (
	"context"
	"crypto/tls"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	evt "maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/0x2142/frigate-notify/config"
)

// sendMatrixSummary отправляет простое текстовое сообщение в Matrix,
// используя retry при неудачах.
func sendMatrixSummary(message string, idx int) error {
	return retrySend("Matrix", idx, DefaultRetry, func() error {
		return trySendMatrix(message, idx)
	})
}

// trySendMatrix — одна попытка отправки. Возвращает ошибку при неудаче.
func trySendMatrix(message string, idx int) error {
	profile := config.ConfigData.Alerts.Matrix[idx]

	m, err := mautrix.NewClient(profile.Server, "", "")
	if err != nil {
		return err
	}

	// Extended timeout, self-signed certs if set
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
		log.Warn().Err(err).Msg("summary: join room")
	}

	_, err = m.SendMessageEvent(context.Background(),
		id.RoomID(profile.RoomID), evt.EventMessage,
		&evt.MessageEventContent{
			MsgType:       evt.MsgText,
			Body:          message,
			Format:        "org.matrix.custom.html",
			FormattedBody: message,
		})
	return err
}
