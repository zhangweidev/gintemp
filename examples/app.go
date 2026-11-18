// 示例：从文件系统加载模板（views/layouts/widgets 三段式目录约定）。
// 在仓库根目录运行：go run ./examples
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

	renderer, err := gintemp.LoadTemplates(
		gintemp.WithTempPath("./examples/templates"), // 模板根路径（相对运行目录）
		gintemp.WithFuncMap(template.FuncMap{ // 注入自定义模板函数
			"add": func(x, y int) int { return x + y },
		}),
		// gintemp.WithReload(true), // 开发期可开启热重载，改模板免重启
	)
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
		// 模板名为相对 views 目录的斜杠路径
		c.HTML(http.StatusOK, "home/index.html", gin.H{})
	})

	r.GET("/hello", Hello)

	r.Run(":8012") // listen and serve on 0.0.0.0:8012 (for windows "localhost:8012")
}
