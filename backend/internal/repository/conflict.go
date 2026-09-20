package repository

import (
	"errors"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

// ErrCorrectionConflict is returned when a second open correction review is created for a
// signed record that already has one, covering both duplicate submissions and races.
var (
	ErrCorrectionConflict = errors.New("an open correction review already exists for this signoff")
	ErrCorrectionNotOpen  = errors.New("the correction review is not open for a decision")
)

// isDuplicateKeyError recognises unique-index violations from MySQL and SQLite. GORM
// normalises MySQL into *mysql.MySQLError (1062); modernc sqlite surfaces a text error.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate") || strings.Contains(message, "unique constraint failed")
}

func modelNow() time.Time { return time.Now().UTC() }
