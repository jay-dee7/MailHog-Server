package api

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	smtp2 "github.com/jay-dee7/MailHog-Server/smtp"
	"github.com/labstack/echo/v4"

	"github.com/ian-kent/go-log/log"
	"github.com/jay-dee7/MailHog-Server/config"
	"github.com/jay-dee7/storage"
	"github.com/mailhog/data"

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
	rawConn     net.Conn
	ln          net.Listener
}

// FIXME should probably move this into APIv1 struct
var stream *goose.EventStream

// ReleaseConfig is an alias to preserve go package API
type ReleaseConfig config.OutgoingSMTP

func (v1 APIv1) sendRawMessage(ctx echo.Context) error {
	tenant, ok := ctx.Get("tenant").(string)
	if !ok || tenant == "" {
		log.Println("tenant id is not present in context")
		return ctx.JSON(http.StatusBadRequest, echo.Map{"error": "tenant id is missing"})
	}

	conn, err := v1.ln.Accept()
	if err != nil {
		log.Printf("[SMTP] Error accepting connection: %s\n", err)
		return ctx.JSON(http.StatusBadRequest, echo.Map{
			"error": err.Error(),
		})
	}
	defer conn.Close()

	if v1.config.Monkey != nil {
		ok := v1.config.Monkey.Accept(conn)
		if !ok {
			_ = conn.Close()
			return ctx.JSON(http.StatusBadRequest, echo.Map{
				"error": "",
			})
		}
	}

	smtp2.Accept(
		conn.(*net.TCPConn).RemoteAddr().String(),
		io.ReadWriteCloser(conn),
		v1.config.Storage,
		v1.config.MessageChan,
		v1.config.Hostname,
		v1.config.Monkey,
		tenant,
	)

	return ctx.JSON(http.StatusOK, echo.Map{"message": "email sent"})
}

func createAPIv1(conf *config.Config, group *echo.Group) *APIv1 {
	log.Printf("[SMTP] Binding to address: %s\n", conf.SMTPBindAddr)
	ln, err := net.Listen("tcp", conf.SMTPBindAddr)
	if err != nil {
		log.Fatalf("[SMTP] Error listening on socket: %s\n", err)
	}

	v1 := &APIv1{
		config:      conf,
		messageChan: make(chan *data.Message),
		ln:          ln,
	}

	stream = goose.NewEventStream()

	v1Group := group.Group(conf.WebPath + "/api/v1")
	msgGroup := v1Group.Group("/messages")

	v1Group.Add(http.MethodGet, "/accept", v1.sendRawMessage)
	v1Group.Add(http.MethodGet, conf.WebPath+"/events", v1.eventStream)

	v1Group.Add(http.MethodGet, "/messages", v1.messages)
	v1Group.Add(http.MethodDelete, "/messages", v1.deleteAll)

	msgGroup.Add(http.MethodGet, "/:id", v1.message)
	msgGroup.Add(http.MethodDelete, "/:id", v1.deleteOne)
	msgGroup.Add(http.MethodGet, "/:id/download", v1.download)
	msgGroup.Add(http.MethodGet, "/:id/mime/part/:part/download", v1.downloadPart)
	msgGroup.Add(http.MethodPost, "/:id/release", v1.releaseOne)

	go func() {
		ticker := time.Tick(time.Minute)
		for {
			select {
			case msg := <-v1.messageChan:
				log.Println("Got message in APIv1 event stream")
				bytes, err := json.MarshalIndent(msg, "", "  ")
				if err != nil {
					log.Printf("error in marshalIndent: %s", err)
					continue
				}
				v1.broadcast(bytes)
			case <-ticker:
				v1.keepalive()
			}
		}
	}()

	return v1
}

func (v1 *APIv1) broadcast(json []byte) {
	log.Println("[APIv1] BROADCAST /api/v1/events")
	stream.Notify("data", json)
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
	tenant, ok := ctx.Get("tenant").(string)
	if !ok {
		return ctx.JSON(http.StatusPreconditionRequired, echo.Map{
			"error": "missing tenant id in request context",
		})
	}

	messages, err := v1.config.Storage.(*storage.MultiTenantMongoDB).List(0, 1000, tenant)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, ErrorResp{Error: err.Error()})
	}
	return ctx.JSON(http.StatusOK, messages)
}

func (v1 *APIv1) message(ctx echo.Context) error {
	id := ctx.Param("id")
	tenant := ctx.Get("tenant").(string)

	message, err := v1.config.Storage.Load(id, tenant)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, ErrorResp{Error: err.Error()})
	}

	return ctx.JSON(http.StatusOK, message)
}

func (v1 *APIv1) download(ctx echo.Context) error {
	id := ctx.QueryParams().Get(":id")
	tenant, ok := ctx.Get("tenant").(string)
	if !ok {
		return ctx.JSON(http.StatusPreconditionRequired, echo.Map{
			"error": "missing tenant id in request context",
		})
	}

	ctx.Response().Header().Set("Content-Type", "message/rfc822")
	ctx.Response().Header().Set("Content-Disposition", "attachment; filename=\""+id+".eml\"")

	message, err := v1.config.Storage.(*storage.MultiTenantMongoDB).Load(id, tenant)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}
	for h, l := range message.Content.Headers {
		for _, v := range l {
			_, _ = ctx.Response().Write([]byte(h + ": " + v + "\r\n"))
		}
	}
	_, _ = ctx.Response().Write([]byte("\r\n" + message.Content.Body))

	return nil
}

func (v1 *APIv1) downloadPart(ctx echo.Context) error {
	id := ctx.Param("id")
	part := ctx.Param("part")

	ctx.Response().Header().Set("Content-Disposition", "attachment; filename=\""+id+"-part-"+part+"\"")
	tenant, ok := ctx.Get("tenant").(string)
	if !ok {
		return ctx.JSON(http.StatusPreconditionRequired, echo.Map{
			"error": "missing tenant id in request context",
		})
	}

	message, _ := v1.config.Storage.Load(id, tenant)
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
	tenant, ok := ctx.Get("tenant").(string)
	if !ok {
		return ctx.JSON(http.StatusPreconditionRequired, echo.Map{
			"error": "missing tenant id in request context",
		})
	}

	err := v1.config.Storage.DeleteAll(tenant)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, ErrorResp{Error: err.Error()})
	}

	return ctx.JSON(http.StatusOK, nil)
}

func (v1 *APIv1) releaseOne(ctx echo.Context) error {
	id := ctx.Param("id")
	tenant, ok := ctx.Get("tenant").(string)
	if !ok {
		return ctx.JSON(http.StatusPreconditionRequired, echo.Map{
			"error": "missing tenant id in request context",
		})
	}

	msg, err := v1.config.Storage.Load(id, tenant)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, ErrorResp{Error: err.Error()})
	}

	var cfg ReleaseConfig
	if err = json.NewDecoder(ctx.Request().Body).Decode(&cfg); err != nil {
		return ctx.JSON(http.StatusInternalServerError, ErrorResp{Error: err.Error()})
	}
	defer ctx.Request().Body.Close()

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
	id := ctx.Param("id")
	tenant, ok := ctx.Get("tenant").(string)
	if !ok {
		return ctx.JSON(http.StatusPreconditionRequired, echo.Map{
			"error": "missing tenant id in request context",
		})
	}

	err := v1.config.Storage.DeleteOne(id, tenant)
	if err != nil {
		ctx.Logger().Print(err.Error())
		return ctx.JSON(http.StatusInternalServerError, ErrorResp{Error: err.Error()})
	}

	return ctx.JSON(http.StatusOK, nil)
}
