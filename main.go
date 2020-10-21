package main

import (
	"context"
	"flag"
	"github.com/labstack/echo/v4"
	"time"

	"github.com/jay-dee7/MailHog-Server/api"
	"github.com/jay-dee7/MailHog-Server/config"
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
	//e.HidePort = true
	//e.HidePort = true

	defer e.Shutdown(ctx)

	router := e.Group("")
	api.CreateAPI(conf, router)

	go func() {
		apiServerSig <- e.Start(conf.APIBindAddr)
	}()

	//go smtp.Listen(conf)

	e.Logger.Printf("api server stopped: %q", <-apiServerSig)
}
