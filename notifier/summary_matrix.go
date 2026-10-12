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

// sendMatrixSummary отправляет простое текстовое сообщение в Matrix.
func sendMatrixSummary(message string, idx int) error {
	profile := config.ConfigData.Alerts.Matrix[idx]

	m, err := mautrix.NewClient(profile.Server, "", "")
	if err != nil {
		return err
	}

	if profile.Insecure {
		m.Client = &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
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

	// Присоединяемся к комнате (на случай, если ещё не в ней)
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
