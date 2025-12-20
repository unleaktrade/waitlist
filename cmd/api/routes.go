package main

import (
	"bytes"
	"embed"
	"encoding/csv"
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"github.com/unleaktrade/waitlist/internal/data"
)

//go:embed templates
var tfs embed.FS

//go:embed swagger/swagger.json
var swaggerFS embed.FS

func setupRouter(app *App) *gin.Engine {
	r := gin.Default()
	t := template.Must(template.ParseFS(tfs, "templates/*"))
	r.SetHTMLTemplate(t)

	r.GET("/openapi.json", func(c *gin.Context) {
		b, err := swaggerFS.ReadFile("swagger/swagger.json")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "swagger spec not found"})
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", b)
	})
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler, ginSwagger.URL("/openapi.json")))

	api := r.Group("/")
	api.Use(app.cors, app.limit, app.requireAPIKey)
	api.GET("/health", func(c *gin.Context) {
		// Minimal, standard JSON health shape
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	})
	api.GET("/:path1/:path2/list", app.list)
	api.POST("/register", app.register)
	api.POST("/activate/:token/:hash", app.activate)
	api.GET("/check-wallet/:address", app.checkWallet)
	return r
}

var jwtregexp = regexp.MustCompile(`^[A-Za-z0-9-_]+\.[A-Za-z0-9-_]+\.[A-Za-z0-9-_]*$`)

func generateSecuredLink(t string) string {
	return fmt.Sprintf("https://unleak.trade/activate/%s", t)
}

func (app *App) register(c *gin.Context) {
	var u data.User
	if err := c.ShouldBindJSON(&u); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	token, err := app.jwt.Create(&u, time.Now())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	hash := app.jwt.Hash(token)
	app.wg.Add(1)
	go func() {
		defer app.wg.Done()
		sl := generateSecuredLink(token)
		app.mailer.SendActivationEmail(u.Email, sl, hash)
	}()

	r := gin.H{
		"hash": hash,
	}
	if gin.IsDebugging() {
		r["token"] = token
	}
	c.JSON(http.StatusAccepted, r)
}

func (app *App) checkWallet(c *gin.Context) {
	a := c.Param("address")
	if !app.c.IsPresent(a) {
		c.JSON(http.StatusNotFound, gin.H{"registered": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"registered": true})
}

func (app *App) requireAPIKey(c *gin.Context) {
	k := c.GetHeader("UNLK-API-KEY")
	if k == "" || k != app.apiKey {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	c.Next()
}

func (app *App) activate(c *gin.Context) {
	t := c.Param("token")
	h := c.Param("hash")
	if !jwtregexp.MatchString(t) || app.jwt.Hash(t) != h {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	u, err := app.jwt.Extract(t) // verify + extract
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	ra, err := app.db.IsPresent(u.Address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if ra {
		err := fmt.Sprintf("user address %s already used", u.Address)
		c.JSON(http.StatusConflict, gin.H{"error": err})
		return
	}

	rs, err := app.db.IsPresent(u.Sponsor)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !rs {
		err := fmt.Sprintf("sponsor address %s not found", u.Sponsor)
		c.JSON(http.StatusBadRequest, gin.H{"error": err})
		return
	}
	e := u.Email         // user's email will be replaced by encryted value, so better do a copy
	err = app.db.Save(u) //user data are replaced by saved one
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// update cache
	app.c.Add(u.Address, u.Timestamp)

	app.wg.Add(1)
	go func() {
		defer app.wg.Done()
		app.mailer.SendConfirmationEmail(e)
	}()

	c.JSON(http.StatusCreated, u)
}

func (app *App) limit(c *gin.Context) {
	ip := c.ClientIP()
	l := app.rl.GetAccess(ip)
	if !l.Allow() {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": "Too Many Requests",
			"ip":    ip,
		})
		return
	}
	c.Next()
}

func (app *App) cors(c *gin.Context) {
	c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
	c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
	c.Writer.Header().Set("Access-Control-Allow-Headers", "origin, content-type, accept, authorization")
	c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")

	if c.Request.Method == "OPTIONS" {
		c.AbortWithStatus(http.StatusNoContent)
		return
	}
	c.Next()
}

func (app *App) list(c *gin.Context) {
	p1, p2 := c.Param("path1"), c.Param("path2")
	if p1 != app.secpath1 || p2 != app.secpath2 {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}

	options := []int{}
	offset := c.Query("offset")
	if offset != "" {
		v, err := strconv.Atoi(offset)
		if err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		options = append(options, v)
	}
	if max := c.Query("max"); max != "" {
		v, err := strconv.Atoi(max)
		if err != nil || offset == "" { // offset & max required
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		options = append(options, v)
	}

	users, err := app.db.List(options...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	mime := c.DefaultQuery("mime", "json")
	switch mime {
	case "csv":
		b := new(bytes.Buffer)
		w := csv.NewWriter(b)
		err := w.Write([]string{"address", "email", "uuid", "timestamp", "sponsor"})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for _, u := range users {
			l, _ := time.LoadLocation("Europe/Paris")
			err := w.Write([]string{u.Address, u.Email, u.UUID, time.UnixMilli(u.Timestamp).In(l).String(), u.Sponsor})
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
		w.Flush()
		c.Header("Content-Description", "File Transfer")
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=users_list_%s.csv", time.Now().Format("20060102-150405")))
		c.Data(http.StatusOK, "text/csv", b.Bytes())
		// c.Writer.Write(b.Bytes())
		return
	default:
		c.JSON(http.StatusOK, gin.H{
			"users": users,
			"count": len(users),
		})
		return
	}
}
