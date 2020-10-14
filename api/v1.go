package api

import (
	"encoding/base64"
	"encoding/json"
	"github.com/labstack/echo/v4"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/ian-kent/go-log/log"
	"github.com/mailhog/MailHog-Server/config"
	"github.com/mailhog/data"
	"github.com/mailhog/storage"

	"github.com/ian-kent/goose"
)

// APIv1 implements version 1 of the MailHog API
//
// The specification has been frozen and will eventually be deprecated.
// Only bug fixes and non-breaking changes will be applied here.
//
// Any changes/additions should be added in APIv2.
type APIv1 struct {
	config      *config.Config
	messageChan chan *data.Message
}

// FIXME should probably move this into APIv1 struct
var stream *goose.EventStream

// ReleaseConfig is an alias to preserve go package API
type ReleaseConfig config.OutgoingSMTP

func createAPIv1(conf *config.Config, router *echo.Group) *APIv1 {
	v1 := &APIv1{
		config:      conf,
		messageChan: make(chan *data.Message),
	}

	stream = goose.NewEventStream()

	router.Add(http.MethodGet, conf.WebPath+"/api/v1/messages", v1.messages)
	router.Add(http.MethodDelete, conf.WebPath+"/api/v1/messages", v1.deleteAll)
	router.Add(http.MethodGet, conf.WebPath+"/api/v1/messages/:id", v1.message)
	router.Add(http.MethodDelete, conf.WebPath+"/api/v1/messages/:id", v1.deleteOne)
	router.Add(http.MethodGet, conf.WebPath+"/api/v1/messages/:id/download", v1.download)
	router.Add(http.MethodGet, conf.WebPath+"/api/v1/messages/:id/mime/part/:part/download", v1.downloadPart)
	router.Add(http.MethodPost, conf.WebPath+"/api/v1/messages/:id/release", v1.releaseOne)
	router.Add(http.MethodGet, conf.WebPath+"/api/v1/events", v1.eventStream)

	go func() {
		for {
			select {
			case msg := <-v1.messageChan:
				log.Println("Got message in APIv1 event stream")
				bytes, err := json.MarshalIndent(msg, "", "  ")
				if err != nil {
					log.Printf("error in marshalIndent: %s", err)
					continue
				}
				strContent := string(bytes)
				log.Printf("Sending content: %s\n", strContent)
				v1.broadcast(strContent)
			case <-time.Tick(time.Minute):
				v1.keepalive()
			}
		}
	}()

	return v1
}

func (v1 *APIv1) defaultOptions(w http.ResponseWriter, req *http.Request) {
	if len(v1.config.CORSOrigin) > 0 {
		w.Header().Add("Access-Control-Allow-Origin", v1.config.CORSOrigin)
		w.Header().Add("Access-Control-Allow-Methods", "OPTIONS,GET,POST,DELETE")
		w.Header().Add("Access-Control-Allow-Headers", "Content-Type")
	}
}

func (v1 *APIv1) broadcast(json string) {
	log.Println("[APIv1] BROADCAST /api/v1/events")
	b := []byte(json)
	stream.Notify("data", b)
}

// keepalive sends an empty keep alive message.
//
// This not only can keep connections alive, but also will detect broken
// connections. Without this it is possible for the server to become
// unresponsive due to too many open files.
func (v1 *APIv1) keepalive() {
	log.Println("[APIv1] KEEPALIVE /api/v1/events")
	stream.Notify("keepalive", []byte{})
}

func (v1 *APIv1) eventStream(ctx echo.Context) error {
	_, _ = stream.AddReceiver(ctx.Response())
	return nil
}

func (v1 *APIv1) messages(ctx echo.Context) error {
	// TODO start, limit
	switch v1.config.Storage.(type) {
	case *storage.MongoDB:
		messages, err := v1.config.Storage.(*storage.MongoDB).List(0, 1000)
		if err != nil {
			return ctx.JSON(http.StatusInternalServerError, ErrorResp{Error: err.Error()})
		}
		return ctx.JSON(http.StatusOK, messages)
	case *storage.InMemory:
		messages, err := v1.config.Storage.(*storage.InMemory).List(0, 1000)
		if err != nil {
			return ctx.JSON(http.StatusInternalServerError, ErrorResp{Error: err.Error()})
		}
		return ctx.JSON(http.StatusOK, messages)
	default:
		return ctx.JSON(http.StatusInternalServerError, ErrorResp{Error: "storage type not supported"})
	}
}

func (v1 *APIv1) message(ctx echo.Context) error {
	id := ctx.QueryParams().Get(":id")

	message, err := v1.config.Storage.Load(id)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, ErrorResp{Error: err.Error()})
	}

	return ctx.JSON(http.StatusOK, message)
}

func (v1 *APIv1) download(ctx echo.Context) error {
	id := ctx.QueryParams().Get(":id")

	ctx.Response().Header().Set("Content-Type", "message/rfc822")
	ctx.Response().Header().Set("Content-Disposition", "attachment; filename=\""+id+".eml\"")

	switch v1.config.Storage.(type) {
	case *storage.MongoDB:
		message, _ := v1.config.Storage.(*storage.MongoDB).Load(id)
		for h, l := range message.Content.Headers {
			for _, v := range l {
				_, _ = ctx.Response().Write([]byte(h + ": " + v + "\r\n"))
			}
		}
		_, _ = ctx.Response().Write([]byte("\r\n" + message.Content.Body))
	case *storage.InMemory:
		message, _ := v1.config.Storage.(*storage.InMemory).Load(id)
		for h, l := range message.Content.Headers {
			for _, v := range l {
				_, _ = ctx.Response().Write([]byte(h + ": " + v + "\r\n"))
			}
		}
		_, _ = ctx.Response().Write([]byte("\r\n" + message.Content.Body))
	default:
		return ctx.JSON(http.StatusInternalServerError, ErrorResp{Error: "storage type not supported"})
	}

	return nil
}

func (v1 *APIv1) downloadPart(ctx echo.Context) error {
	id := ctx.QueryParams().Get(":id")
	part := ctx.QueryParams().Get(":part")

	ctx.Response().Header().Set("Content-Disposition", "attachment; filename=\""+id+"-part-"+part+"\"")

	message, _ := v1.config.Storage.Load(id)
	contentTransferEncoding := ""
	pid, _ := strconv.Atoi(part)
	for h, l := range message.MIME.Parts[pid].Headers {
		for _, v := range l {
			switch strings.ToLower(h) {
			case "content-disposition":
				// Prevent duplicate "content-disposition"
				ctx.Response().Header().Set(h, v)
			case "content-transfer-encoding":
				if contentTransferEncoding == "" {
					contentTransferEncoding = v
				}
				fallthrough
			default:
				ctx.Response().Header().Add(h, v)
			}
		}
	}
	body := []byte(message.MIME.Parts[pid].Body)
	if strings.ToLower(contentTransferEncoding) == "base64" {
		var e error
		body, e = base64.StdEncoding.DecodeString(message.MIME.Parts[pid].Body)
		if e != nil {
			log.Printf("[APIv1] Decoding base64 encoded body failed: %s", e)
		}
	}

	_, _ = ctx.Response().Write(body)
	return nil
}

func (v1 *APIv1) deleteAll(ctx echo.Context) error {
	err := v1.config.Storage.DeleteAll()
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, ErrorResp{Error: err.Error()})
	}

	return ctx.JSON(http.StatusOK, nil)
}

func (v1 *APIv1) releaseOne(ctx echo.Context) error {
	id := ctx.QueryParams().Get(":id")

	msg, err := v1.config.Storage.Load(id)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, ErrorResp{Error: err.Error()})
	}

	var cfg ReleaseConfig
	if err = json.NewDecoder(ctx.Request().Body).Decode(&cfg); err != nil {
		return ctx.JSON(http.StatusInternalServerError, ErrorResp{Error: err.Error()})
	}
	ctx.Logger().Printf("%+v", cfg)

	ctx.Logger().Printf("Got message: %s", msg.ID)

	if cfg.Save {
		if _, ok := v1.config.OutgoingSMTP[cfg.Name]; ok {
			ctx.Logger().Printf("Server already exists named %s", cfg.Name)
			return ctx.JSON(http.StatusBadRequest, ErrorResp{Error: "Server already exists named " + cfg.Name})
		}

		cf := config.OutgoingSMTP(cfg)
		v1.config.OutgoingSMTP[cfg.Name] = &cf
		ctx.Logger().Printf("Saved server with name %s", cfg.Name)
	}

	if len(cfg.Name) > 0 {
		if c, ok := v1.config.OutgoingSMTP[cfg.Name]; ok {
			ctx.Logger().Printf("Using server with name: %s", cfg.Name)
			cfg.Name = c.Name
			if len(cfg.Email) == 0 {
				cfg.Email = c.Email
			}
			cfg.Host = c.Host
			cfg.Port = c.Port
			cfg.Username = c.Username
			cfg.Password = c.Password
			cfg.Mechanism = c.Mechanism
		} else {
			ctx.Logger().Printf("Server not found: %s", cfg.Name)
			return ctx.JSON(http.StatusBadRequest, nil)
		}
	}

	ctx.Logger().Printf("Releasing to %s (via %s:%s)", cfg.Email, cfg.Host, cfg.Port)

	bytes := make([]byte, 0)
	for h, l := range msg.Content.Headers {
		for _, v := range l {
			bytes = append(bytes, []byte(h+": "+v+"\r\n")...)
		}
	}
	bytes = append(bytes, []byte("\r\n"+msg.Content.Body)...)

	var auth smtp.Auth

	if len(cfg.Username) > 0 || len(cfg.Password) > 0 {
		log.Printf("Found username/password, using auth mechanism: [%s]", cfg.Mechanism)
		switch cfg.Mechanism {
		case "CRAMMD5":
			auth = smtp.CRAMMD5Auth(cfg.Username, cfg.Password)
		case "PLAIN":
			auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		default:
			log.Printf("Error - invalid authentication mechanism")
			return ctx.JSON(http.StatusBadRequest, nil)
		}
	}

	err = smtp.SendMail(cfg.Host+":"+cfg.Port, auth, "nobody@"+v1.config.Hostname, []string{cfg.Email}, bytes)
	if err != nil {
		log.Printf("Failed to release message: %s", err)
		return ctx.JSON(http.StatusInternalServerError, nil)
	}
	log.Printf("Message released successfully")
	return nil
}

func (v1 *APIv1) deleteOne(ctx echo.Context) error {
	id := ctx.QueryParams().Get(":id")

	err := v1.config.Storage.DeleteOne(id)
	if err != nil {
		ctx.Logger().Print(err.Error())
		return ctx.JSON(http.StatusInternalServerError, ErrorResp{Error: err.Error()})
	}

	return ctx.JSON(http.StatusOK, nil)
}
