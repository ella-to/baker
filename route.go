package baker

import "context"

// RouteInfo carries the matched route pattern back to an outer handler (for
// example the OTel metrics middleware) after baker has resolved a request to a
// registered endpoint. Using the matched pattern instead of the raw request
// path keeps metric/label cardinality bounded by the number of registered
// routes rather than by the number of distinct URLs seen.
type RouteInfo struct {
	// Pattern is the matched "domain+path" route, or empty if no route matched.
	Pattern string
}

type routeInfoKeyType struct{}

var routeInfoKey routeInfoKeyType

// WithRouteInfo returns a context carrying a fresh RouteInfo and the pointer to
// it. Wrap the request context before invoking baker, then read RouteInfo.Pattern
// afterwards to learn which route (if any) handled the request.
func WithRouteInfo(ctx context.Context) (context.Context, *RouteInfo) {
	ri := &RouteInfo{}
	return context.WithValue(ctx, routeInfoKey, ri), ri
}

func routeInfoFromContext(ctx context.Context) *RouteInfo {
	ri, _ := ctx.Value(routeInfoKey).(*RouteInfo)
	return ri
}
