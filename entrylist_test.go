package baker_test

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"ella.to/baker"
	"ella.to/baker/rule"
)

func TestBakerEndpoints(t *testing.T) {
	{

		rr := httptest.NewRecorder()
		baker.NewEntryList().
			New("example.com", "/", true).
			WriteResponse(rr)
		assert.JSONEq(t, `{"endpoints":[{"domain":"example.com","path":"/","rules":[]}]}`, strings.TrimSpace(rr.Body.String()))
	}

	{
		rr := httptest.NewRecorder()
		baker.NewEntryList().
			New("example.com", "/", true).
			WithRules(rule.NewAppendPath("a", "b")).
			WriteResponse(rr)
		assert.JSONEq(t, `{"endpoints":[{"domain":"example.com","path":"/","rules":[{"args":{"begin":"a","end":"b"},"type":"AppendPath"}]}]}`, strings.TrimSpace(rr.Body.String()))
	}

	{
		rr := httptest.NewRecorder()
		baker.NewEntryList().
			New("example.com", "/", true).
			WithRules(
				rule.NewAppendPath("a", "b"),
				rule.NewReplacePath("/a", "/b", 1),
			).
			WriteResponse(rr)
		assert.JSONEq(t, `{"endpoints":[{"domain":"example.com","path":"/","rules":[{"args":{"begin":"a","end":"b"},"type":"AppendPath"},{"args":{"search":"/a","replace":"/b","times":1},"type":"ReplacePath"}]}]}`, strings.TrimSpace(rr.Body.String()))
	}

	{
		rr := httptest.NewRecorder()
		baker.NewEntryList().
			New("example.com", "/", true).
			WithRules(
				rule.NewRateLimiter(1, 1*time.Second),
			).
			WriteResponse(rr)
		fmt.Println(strings.TrimSpace(rr.Body.String()))
		assert.JSONEq(t, `{"endpoints":[{"domain":"example.com","path":"/","rules":[{"type":"RateLimiter","args":{"request_limit":1,"window_duration":"1s"}}]}]}`, strings.TrimSpace(rr.Body.String()))
	}
}
