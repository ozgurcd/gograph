package answers179

import (
	"github.com/gin-gonic/gin"
	"os"
)

func Register(r *gin.RouterGroup) { r.DELETE("/records/:id", DeleteFactory()) }
func DeleteFactory() gin.HandlerFunc {
	return func(*gin.Context) { deleteRecord() }
}
func deleteRecord() { auditRecord() }
func auditRecord()  {}

func Lookup(name string) (string, bool) { return os.LookupEnv(name) }
func Get(name string) string            { return os.Getenv(name) }
func Literal() string                   { return os.Getenv("FIXED_SETTING") }
func NoEnvironment()                    {}
