package minimax

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

// gin.SetMode writes package-level globals that gin.New reads. Tests here run
// in parallel, so setting the mode inside one of them races with another's
// setup. The mode belongs to the whole test binary, so it is set once here.
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}
