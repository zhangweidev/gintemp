package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/xxiu/gintemp"
)

func Hello(c *gin.Context) {

	c.HTML(http.StatusOK, "auth/login.html", gin.H{})
}

func main() {
	r := gin.Default()

	r.HTMLRender = gintemp.LoadTemplates()

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
		})
	})

	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "home/index.html", gin.H{})
	})

	r.Run() // listen and serve on 0.0.0.0:8080 (for windows "localhost:8080")
}
