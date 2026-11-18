// 示例：用 go:embed 将模板编译进二进制，实现单文件部署。
// 目录结构约定（views/layouts/widgets）与文件系统模式一致。
package main

import (
	"embed"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zhangweidev/gintemp"
)

//go:embed templates
var templatesFS embed.FS

func main() {
	r := gin.Default()

	renderer, err := gintemp.LoadTemplates(
		gintemp.WithFS(templatesFS), // 模板随二进制打包，无需外部模板目录
	)
	if err != nil {
		log.Fatal(err)
	}
	r.HTMLRender = renderer

	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "home/index.html", gin.H{"Title": "gintemp embed"})
	})

	r.Run(":8013") // listen and serve on 0.0.0.0:8013
}
