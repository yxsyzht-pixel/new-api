package helper

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

// gin.SetMode writes package-level globals. Tests here run in parallel, and
// each calling it is a data race the detector reports against itself. The mode
// belongs to the whole test binary, so it is set once here.
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}
