package main

import (
	"context"
	"flag"
	"github.com/labstack/echo/v4"
	"time"

	"github.com/jay-dee7/MailHog-Server/api"
	"github.com/jay-dee7/MailHog-Server/config"
	mhconfig "github.com/mailhog/MailHog-Server/config"
	"github.com/jay-dee7/MailHog-Server/smtp"
	comcfg "github.com/mailhog/MailHog/config"
	"github.com/mailhog/http"
)

func configureCliFlags(multiTenant bool) (*config.Config, *comcfg.Config) {
	comcfg.RegisterFlags()
	config.RegisterFlags()
	flag.Parse()
	return config.Configure(multiTenant), comcfg.Configure()
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	conf, comconf := configureCliFlags(true)

	if comconf.AuthFile != "" {
		http.AuthFile(comconf.AuthFile)
	}

	apiServerSig := make(chan error)

	e := echo.New()
	defer e.Shutdown(ctx)

	router := e.Group("")
	api.CreateAPI(conf, router)

	go func() {
		apiServerSig <- e.Start(conf.APIBindAddr)
	}()

	sig := make(chan int)

	c := &mhconfig.Config{
		SMTPBindAddr:    conf.SMTPBindAddr,
		APIBindAddr:      conf.APIBindAddr,
		Hostname:         conf.Hostname,
		MongoURI:         conf.MongoURI,
		MongoDb:          conf.MongoDb,
		MongoColl:        conf.MongoColl,
		StorageType:      conf.StorageType,
		CORSOrigin:       conf.CORSOrigin,
		MaildirPath:      conf.MaildirPath,
		InviteJim:        false,
		Storage:          conf.SimpleStorage,
		MessageChan:      conf.MessageChan,
		Assets:           conf.Assets,
		Monkey:           nil,
		OutgoingSMTPFile: "",
		OutgoingSMTP:     nil,
		WebPath:          conf.WebPath,
	}
	_ = c

	go smtp.Listen(conf, sig)

	e.Logger.Printf("api server stopped: %q", <-apiServerSig)
}
