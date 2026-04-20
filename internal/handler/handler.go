package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/walker-qiang/personal-finance/internal/db/store"
	"github.com/walker-qiang/personal-finance/internal/publish"
)

type API struct {
	Store *store.Store
	Job   *publish.Job
}

func (a *API) Register(r *gin.Engine) {
	r.GET("/healthz", a.healthz)

	g := r.Group("/api/finance")
	{
		g.GET("/assets", a.listAssets)
		g.GET("/snapshots", a.listSnapshots)
		g.GET("/transactions", a.listTransactions)
		g.GET("/holdings", a.listHoldings)
		g.POST("/publish", a.runPublish)
	}
}

func (a *API) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"ts": time.Now().UTC().Format(time.RFC3339),
	})
}

func (a *API) listAssets(c *gin.Context) {
	rows, err := a.Store.ListAssets(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"assets": rows, "count": len(rows)})
}

func (a *API) listSnapshots(c *gin.Context) {
	rows, err := a.Store.ListSnapshots(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"snapshots": rows, "count": len(rows)})
}

func (a *API) listTransactions(c *gin.Context) {
	rows, err := a.Store.ListTransactions(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"transactions": rows, "count": len(rows)})
}

func (a *API) listHoldings(c *gin.Context) {
	rows, err := a.Store.ListHoldings(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"holdings": rows, "count": len(rows)})
}

func (a *API) runPublish(c *gin.Context) {
	res := a.Job.Run(c.Request.Context())
	status := http.StatusOK
	if !res.OK {
		status = http.StatusInternalServerError
	}
	c.JSON(status, res)
}
