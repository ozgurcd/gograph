package fixture

import (
	"example.com/answers178/nested"
	"net/url"
	"os"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func Register(router *gin.RouterGroup, unknown gin.HandlerFunc) {
	g := router.Group("/api")
	g.DELETE("/:id", DeleteFactory())
	g.DELETE("/fallback", MissingFactory())
	g.DELETE("/unknown", UnknownFactory(unknown))
	g.DELETE("/named", NamedFactory())
	g.DELETE("/nested", nested.Factory())
	zap.Any("org_id", 1)
	values := url.Values{}
	_ = values.Get("client_id")
}

func DeleteFactory() gin.HandlerFunc {
	setupOnly()
	return func(c *gin.Context) {
		deleteRecord()
		_ = os.Getenv("HANDLER_SETTING")
		c.JSON(200, nil)
	}
}
func MissingFactory() gin.HandlerFunc {
	return func(c *gin.Context) { c.JSON(503, nil) }
}
func UnknownFactory(h gin.HandlerFunc) gin.HandlerFunc { return h }
func NamedFactory() gin.HandlerFunc                    { return namedHandler }
func namedHandler(c *gin.Context)                      { deleteRecord(); c.JSON(200, nil) }
func setupOnly()                                       {}
func deleteRecord()                                    {}

var ErrLimited = os.ErrPermission

func useError() error { return ErrLimited }
func Setting(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	return getenv("FIXTURE_SETTING")
}

type Service struct{}

func (*Service) Callback() {}
