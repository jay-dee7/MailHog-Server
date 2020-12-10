package api

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/ian-kent/go-log/log"
	"github.com/jay-dee7/MailHog-Server/config"
	"github.com/mailhog/MailHog-Server/websockets"
	"github.com/mailhog/data"
)

// APIv2 implements version 2 of the MailHog API
//
// It is currently experimental and may change in future releases.
// Use APIv1 for guaranteed compatibility.
type APIv2 struct {
	config      *config.Config
	messageChan chan *data.Message
	wsHub       *websockets.Hub
}

type ErrorResp struct {
	Error string `json:"error,omitempty"`
}

func createAPIv2(conf *config.Config, group *echo.Group) *APIv2 {
	v2 := &APIv2{
		config:      conf,
		messageChan: make(chan *data.Message),
		wsHub:       websockets.NewHub(),
	}

	// v1Group := group.Group(conf.WebPath + "/api/v2")

	group.Add(http.MethodGet, conf.WebPath+"/api/v2/messages", v2.messages)
	group.Add(http.MethodGet, conf.WebPath+"/api/v2/search", v2.search)
	group.Add(http.MethodGet, conf.WebPath+"/api/v2/outgoing-smtp", v2.listOutgoingSMTP)
	group.Add(http.MethodGet, conf.WebPath+"/api/v2/websocket", v2.websocket)

	go func() {
		for {
			select {
			case msg := <-v2.messageChan:
				log.Println("Got message in APIv2 websocket channel")
				v2.broadcast(msg)
			}
		}
	}()

	return v2
}

type messagesResult struct {
	Total int            `json:"total"`
	Count int            `json:"count"`
	Start int            `json:"start"`
	Items []data.Message `json:"items"`
}

func (v2 *APIv2) getStartLimit(q url.Values) (start, limit int) {
	start = 0
	limit = 50

	s := q.Get("start")
	if n, e := strconv.ParseInt(s, 10, 64); e == nil && n > 0 {
		start = int(n)
	}

	l := q.Get("limit")
	if n, e := strconv.ParseInt(l, 10, 64); e == nil && n > 0 {
		if n > 250 {
			n = 250
		}
		limit = int(n)
	}

	return
}

func (v2 *APIv2) messages(ctx echo.Context) error {
	start, limit := v2.getStartLimit(ctx.QueryParams())

	tenant, ok := ctx.Get("tenant").(string)
	if !ok {
		return ctx.JSON(http.StatusBadRequest, echo.Map{
			"error": "missing tenant id in request context",
		})
	}
	messages, err := v2.config.Storage.List(start, limit, tenant)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, ErrorResp{
			Error: err.Error(),
		})
	}

	res := messagesResult{
		Total: v2.config.Storage.Count(tenant),
		Count: len(*messages),
		Start: start,
		Items: *messages,
	}

	return ctx.JSON(http.StatusOK, res)
}

func (v2 *APIv2) search(ctx echo.Context) error {
	start, limit := v2.getStartLimit(ctx.QueryParams())

	kind := ctx.QueryParams().Get("kind")
	if kind != "from" && kind != "to" && kind != "containing" {
		return ctx.JSON(http.StatusBadRequest, ErrorResp{
			Error: "invalid search param: kind",
		})
	}

	query := ctx.QueryParams().Get("query")
	if len(query) == 0 {
		return ctx.JSON(http.StatusBadRequest, ErrorResp{
			Error: "invalid search param: query",
		})
	}

	tenant, ok := ctx.Get("tenant").(string)
	if !ok {
		return ctx.JSON(http.StatusPreconditionRequired, echo.Map{
			"error": "missing tenant id in request context",
		})
	}

	messages, total, err := v2.config.Storage.Search(kind, query, start, limit, tenant)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, ErrorResp{
			Error: err.Error(),
		})
	}
	ctx.Logger().Printf("%s", messages)

	resp := messagesResult{
		Total: total,
		Count: len(*messages),
		Start: start,
		Items: *messages,
	}

	return ctx.JSON(http.StatusOK, resp)
}

func (v2 *APIv2) listOutgoingSMTP(ctx echo.Context) error {
	return ctx.JSON(http.StatusOK, v2.config.OutgoingSMTP)
}

func (v2 *APIv2) websocket(ctx echo.Context) error {
	v2.wsHub.Serve(ctx.Response(), ctx.Request())
	return nil
}

func (v2 *APIv2) broadcast(msg *data.Message) {
	v2.wsHub.Broadcast(msg)
}
