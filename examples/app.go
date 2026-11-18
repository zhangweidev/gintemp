package main

import (
	"html/template"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zhangweidev/gintemp"
)

func Hello(c *gin.Context) {

	c.HTML(http.StatusOK, "auth/login.html", gin.H{})
}

func main() {
	r := gin.Default()

	renderer, err := gintemp.LoadTemplates(gintemp.WithFuncMap(template.FuncMap{
		"add": func(x, y int) int { return x + y },
	}))
	if err != nil {
		log.Fatal(err)
	}
	r.HTMLRender = renderer

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
		})
	})

	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "home/index.html", gin.H{})
	})

	r.GET("/hello", Hello)

	r.Run(":8012") // listen and serve on 0.0.0.0:8012 (for windows "localhost:8012")
}
