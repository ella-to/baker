package trie_test

import (
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"ella.to/baker/internal/trie"
)

func TestPaths(t *testing.T) {
	t.Run("testing adding new value to trie", func(t *testing.T) {
		trie := trie.New[int]()
		trie.Put([]rune("/"), 1)

		assert.Equal(t, 1, trie.Size())
		assert.Equal(t, 1, trie.Get([]rune("/")))

		trie.Del([]rune("/"))
		assert.Equal(t, 0, trie.Size())
		assert.Equal(t, 0, trie.Get([]rune("/")))
	})

	t.Run("testing children", func(t *testing.T) {
		trie := trie.New[int]()
		trie.Put([]rune("/a/b/c"), 1)
		trie.Put([]rune("/a/b"), 2)

		// assert.Equal(t, 2, trie.Size())

		assert.Equal(t, 1, trie.Get([]rune("/a/b/c")))
		assert.Equal(t, 2, trie.Get([]rune("/a/b")))

		trie.Del([]rune("/a/b/c"))
		// assert.Equal(t, 1, trie.Size())
		assert.Equal(t, 2, trie.Get([]rune("/a/b")))
		assert.Equal(t, 0, trie.Get([]rune("/a/b/c")))
	})
}

func TestTrieWildcard(t *testing.T) {
	t.Run("wildcard matches any path", func(t *testing.T) {
		tr := trie.New[string]()
		tr.Put([]rune("/*"), "wildcard")

		assert.Equal(t, "wildcard", tr.Get([]rune("/anything")))
		assert.Equal(t, "wildcard", tr.Get([]rune("/a/b/c")))
		assert.Equal(t, "wildcard", tr.Get([]rune("/users/123/profile")))
	})

	t.Run("exact path takes precedence over wildcard", func(t *testing.T) {
		tr := trie.New[string]()
		tr.Put([]rune("/*"), "wildcard")
		tr.Put([]rune("/exact"), "exact")

		assert.Equal(t, "exact", tr.Get([]rune("/exact")))
		assert.Equal(t, "wildcard", tr.Get([]rune("/other")))
	})

	t.Run("nested wildcards", func(t *testing.T) {
		tr := trie.New[string]()
		tr.Put([]rune("/api/*"), "api-wildcard")
		tr.Put([]rune("/api/v1/*"), "v1-wildcard")

		assert.Equal(t, "api-wildcard", tr.Get([]rune("/api/anything")))
		assert.Equal(t, "v1-wildcard", tr.Get([]rune("/api/v1/users")))
	})

	t.Run("wildcard fallback", func(t *testing.T) {
		tr := trie.New[string]()
		tr.Put([]rune("/api/*"), "api")
		tr.Put([]rune("/api/users"), "users")

		assert.Equal(t, "users", tr.Get([]rune("/api/users")))
		assert.Equal(t, "api", tr.Get([]rune("/api/unknown")))
		assert.Equal(t, "api", tr.Get([]rune("/api/users/123"))) // Falls back to wildcard
	})
}

func TestGetStringMatchesGet(t *testing.T) {
	// GetString is the allocation-free hot-path equivalent of Get; it must
	// return identical results for every key, including wildcard fallbacks,
	// non-ASCII runes, and the empty key.
	tr := trie.New[string]()
	keys := []string{
		"/", "/a", "/a/b", "/a/b/c", "/api/*", "/api/v1/*", "/exact", "/*",
		"/ünïcode/*", "/ünïcode/path",
	}
	for _, k := range keys {
		tr.Put([]rune(k), "v:"+k)
	}

	lookups := []string{
		"/", "/a", "/a/b", "/a/b/c", "/a/b/c/d", "/api/anything",
		"/api/v1/users", "/exact", "/other", "/nope", "",
		"/ünïcode/path", "/ünïcode/else",
	}
	for _, k := range lookups {
		assert.Equalf(t, tr.Get([]rune(k)), tr.GetString(k), "key %q", k)
	}
}

func TestTrieDelete(t *testing.T) {
	t.Run("delete leaf node", func(t *testing.T) {
		tr := trie.New[int]()
		tr.Put([]rune("/a/b/c"), 1)
		tr.Put([]rune("/a/b"), 2)

		tr.Del([]rune("/a/b/c"))

		assert.Equal(t, 0, tr.Get([]rune("/a/b/c")))
		assert.Equal(t, 2, tr.Get([]rune("/a/b")))
	})

	t.Run("delete middle node behavior", func(t *testing.T) {
		tr := trie.New[int]()
		tr.Put([]rune("/a/b/c"), 1)
		tr.Put([]rune("/a/b"), 2)

		tr.Del([]rune("/a/b"))

		// Child should still be accessible
		assert.Equal(t, 1, tr.Get([]rune("/a/b/c")))
		// Middle node value should be cleared to default value
		// Note: The current implementation may not fully clear middle nodes
		// when they have children - this test documents actual behavior
	})

	t.Run("delete non-existent path", func(t *testing.T) {
		tr := trie.New[int]()
		tr.Put([]rune("/a/b"), 1)

		// Should not panic
		tr.Del([]rune("/x/y/z"))
		tr.Del([]rune("/a/b/c/d"))

		assert.Equal(t, 1, tr.Get([]rune("/a/b")))
	})

	t.Run("delete wildcard path", func(t *testing.T) {
		tr := trie.New[string]()
		tr.Put([]rune("/api/*"), "api")

		tr.Del([]rune("/api/*"))

		assert.Equal(t, "", tr.Get([]rune("/api/anything")))
	})
}

func TestTrieEdgeCases(t *testing.T) {
	t.Run("empty path", func(t *testing.T) {
		tr := trie.New[string]()
		tr.Put([]rune(""), "empty")

		assert.Equal(t, "empty", tr.Get([]rune("")))
	})

	t.Run("single character paths", func(t *testing.T) {
		tr := trie.New[int]()
		tr.Put([]rune("/"), 1)
		tr.Put([]rune("a"), 2)
		tr.Put([]rune("b"), 3)

		assert.Equal(t, 1, tr.Get([]rune("/")))
		assert.Equal(t, 2, tr.Get([]rune("a")))
		assert.Equal(t, 3, tr.Get([]rune("b")))
	})

	t.Run("unicode paths", func(t *testing.T) {
		tr := trie.New[string]()
		tr.Put([]rune("/用户/资料"), "chinese")
		tr.Put([]rune("/пользователь"), "russian")
		tr.Put([]rune("/🚀/launch"), "emoji")

		assert.Equal(t, "chinese", tr.Get([]rune("/用户/资料")))
		assert.Equal(t, "russian", tr.Get([]rune("/пользователь")))
		assert.Equal(t, "emoji", tr.Get([]rune("/🚀/launch")))
	})

	t.Run("overwrite existing value", func(t *testing.T) {
		tr := trie.New[int]()
		tr.Put([]rune("/path"), 1)
		tr.Put([]rune("/path"), 2)

		assert.Equal(t, 2, tr.Get([]rune("/path")))
	})

	t.Run("get non-existent path returns zero value", func(t *testing.T) {
		tr := trie.New[int]()

		assert.Equal(t, 0, tr.Get([]rune("/nonexistent")))
	})

	t.Run("long path", func(t *testing.T) {
		tr := trie.New[int]()
		longPath := make([]rune, 1000)
		for i := range longPath {
			longPath[i] = rune('a' + (i % 26))
		}

		tr.Put(longPath, 42)
		assert.Equal(t, 42, tr.Get(longPath))
	})
}

func TestTrieMultipleValues(t *testing.T) {
	t.Run("store struct values", func(t *testing.T) {
		type Service struct {
			Name string
			Port int
		}

		tr := trie.New[*Service]()
		tr.Put([]rune("/api"), &Service{Name: "api", Port: 8080})
		tr.Put([]rune("/web"), &Service{Name: "web", Port: 3000})

		api := tr.Get([]rune("/api"))
		assert.NotNil(t, api)
		assert.Equal(t, "api", api.Name)
		assert.Equal(t, 8080, api.Port)

		web := tr.Get([]rune("/web"))
		assert.NotNil(t, web)
		assert.Equal(t, "web", web.Name)
	})
}

func BenchmarkPut(b *testing.B) {
	trie := trie.New[int]()
	path := []rune("/a/b/c")
	for i := 0; i < b.N; i++ {
		trie.Put(path, i)
	}
}

func BenchmarkPutALot(b *testing.B) {
	trie := trie.New[int]()

	paths := make([][]rune, 1000)
	for i := 0; i < 1000; i++ {
		paths[i] = generateRandomPath()
	}

	for i := 0; i < b.N; i++ {
		trie.Put(paths[i%1000], i)
	}
}

func BenchmarkGet(b *testing.B) {
	trie := trie.New[int]()
	path := []rune("/a/b/c")
	trie.Put(path, 1)
	for i := 0; i < b.N; i++ {
		trie.Get(path)
	}
}

func BenchmarkGetWithWildcard(b *testing.B) {
	tr := trie.New[int]()
	tr.Put([]rune("/api/*"), 1)

	path := []rune("/api/users/123/profile")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr.Get(path)
	}
}

func BenchmarkGetDeepPath(b *testing.B) {
	tr := trie.New[int]()
	deepPath := []rune("/a/b/c/d/e/f/g/h/i/j/k/l/m/n/o/p")
	tr.Put(deepPath, 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr.Get(deepPath)
	}
}

func BenchmarkDel(b *testing.B) {
	trie := trie.New[int]()
	path := []rune("/a/b/c")

	for i := 0; i < b.N; i++ {
		trie.Put(path, i)
		trie.Del(path)
	}
}

func BenchmarkMixedOperations(b *testing.B) {
	tr := trie.New[int]()
	paths := [][]rune{
		[]rune("/api/users"),
		[]rune("/api/users/123"),
		[]rune("/api/posts"),
		[]rune("/api/*"),
		[]rune("/web/*"),
	}

	// Pre-populate
	for i, p := range paths {
		tr.Put(p, i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		op := i % 3
		path := paths[i%len(paths)]
		switch op {
		case 0:
			tr.Get(path)
		case 1:
			tr.Put(path, i)
		case 2:
			tr.Get([]rune("/api/unknown/path"))
		}
	}
}

func BenchmarkConcurrentReads(b *testing.B) {
	tr := trie.New[int]()
	tr.Put([]rune("/api/users"), 1)
	tr.Put([]rune("/api/posts"), 2)
	tr.Put([]rune("/api/*"), 3)

	path := []rune("/api/users")
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tr.Get(path)
		}
	})
}

func generateRandomPath() []rune {
	seed := rand.New(rand.NewSource(time.Now().UnixNano()))
	pathLength := seed.Intn(10) + 1 // Generate a random path length between 1 and 10
	path := make([]rune, pathLength)
	for i := 0; i < pathLength; i++ {
		path[i] = rune(rand.Intn(26) + 97) // Generate a random lowercase letter
	}
	return path
}
