//go:build windows

package diagnostics

import (
	"strings"
	"testing"
	"time"
)

func TestTextKeepsOriginalEvidenceWithoutGuessedCause(t *testing.T) {
	original := "SCM.OpenService\n*syscall.Errno: Access is denied.\ncode=5 hex=0x00000005\nendpoint=server.example:5555"
	report := Report{CreatedAt: time.Now(), Overall: "failure", Checks: []Check{{Name: "SCM", Level: LevelFail, Detail: original}}, Debug: "raw-native-details"}
	text := report.Text()
	if !strings.Contains(text, original) || !strings.Contains(text, "raw-native-details") {
		t.Fatal("raw diagnostic data lost")
	}
	if strings.Contains(text, "Вероятная причина") || strings.Contains(text, "адрес сервера в отчёт не включены") {
		t.Fatal("guessed or incorrect report text")
	}
}
