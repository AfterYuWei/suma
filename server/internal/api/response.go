package api

import "github.com/gin-gonic/gin"

type envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

func success(c *gin.Context, data any) {
	c.JSON(200, envelope{Code: 0, Message: "success", Data: data})
}
func failure(c *gin.Context, status, code int, message string) {
	c.JSON(status, envelope{Code: code, Message: message, Data: nil})
}
