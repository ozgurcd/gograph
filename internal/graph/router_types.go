package graph

// RouterType recognizes the supported framework receiver types, not method
// spellings shared with loggers, URL values, caches, and other APIs.
func RouterType(pkg, name string) bool {
	switch pkg {
	case "github.com/gin-gonic/gin":
		return name == "Engine" || name == "RouterGroup" || name == "IRouter" || name == "IRoutes"
	case "github.com/labstack/echo", "github.com/labstack/echo/v4":
		return name == "Echo" || name == "Group"
	case "github.com/gofiber/fiber", "github.com/gofiber/fiber/v2", "github.com/gofiber/fiber/v3":
		return name == "App" || name == "Group" || name == "Router"
	case "github.com/go-chi/chi", "github.com/go-chi/chi/v5":
		return name == "Mux" || name == "Router"
	case "github.com/gorilla/mux":
		return name == "Router" || name == "Route"
	case "github.com/julienschmidt/httprouter":
		return name == "Router"
	case "net/http":
		return name == "ServeMux"
	}
	return false
}
