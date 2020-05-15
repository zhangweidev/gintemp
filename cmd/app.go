package main

import (
	"github.com/gin-gonic/gin"
	"github.com/xxiu/gintemp"
)

func main() {
	r := gin.Default()

	r.HTMLRender = gintemp.LoadTemplates()

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
		})
	})
	r.Run() // listen and serve on 0.0.0.0:8080 (for windows "localhost:8080")
}
