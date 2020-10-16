package api

import (
	"github.com/jay-dee7/MailHog-Server/config"
	"github.com/labstack/echo/v4"
)

func CreateAPI(conf *config.Config, router *echo.Group) {
	v1 := createAPIv1(conf, router)
	v2 := createAPIv2(conf, router)

	go func() {
		for {
			select {
			case msg := <-conf.MessageChan:
				v1.messageChan <- msg
				v2.messageChan <- msg
			}
		}
	}()
}
