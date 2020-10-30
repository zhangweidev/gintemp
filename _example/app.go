package main

import (
	"html/template"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/xxiu/gintemp"
)

func Hello(c *gin.Context) {

	c.HTML(http.StatusOK, "auth/login.html", gin.H{})
}

func main() {
	r := gin.Default()

	r.HTMLRender = gintemp.LoadTemplates(gintemp.WithFuncMap(template.FuncMap{
		"add": func(x, y int) int { return x + y },
	}))

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
		})
	})

	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "home/index.html", gin.H{})
	})

	r.GET("/hello", Hello)

	r.Run(":8012") // listen and serve on 0.0.0.0:8080 (for windows "localhost:8080")
}
