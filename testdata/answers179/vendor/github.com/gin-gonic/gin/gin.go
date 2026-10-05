package gin

type Context struct{}
type HandlerFunc func(*Context)
type RouterGroup struct{}

func (*Context) JSON(int, any)                     {}
func (*RouterGroup) DELETE(string, ...HandlerFunc) {}
func (g *RouterGroup) Group(string) *RouterGroup   { return g }
