package nested

import "github.com/gin-gonic/gin"

func Factory() gin.HandlerFunc {
	return func(c *gin.Context) { c.JSON(200, nil) }
}
