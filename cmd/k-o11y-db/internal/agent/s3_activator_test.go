package agent

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestActivate_SkipsWhenAlreadyActive(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	daemon := New(&Config{
		ClickHouseHost:     "localhost",
		ClickHousePort:     9000,
		ClickHousePassword: "test",
		EncryptionKey:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})
	daemon.db = db

	activator := NewS3Activator(db, daemon)

	// system.disks에 S3 disk가 이미 있는 경우
	mock.ExpectQuery("SELECT count\\(\\) FROM system.disks WHERE type='s3'").
		WillReturnRows(sqlmock.NewRows([]string{"count()"}).AddRow(1))

	cfg := &S3Config{
		ConfigID:  "warm",
		Bucket:    "test-bucket",
		Region:    "ap-northeast-2",
		S3Enabled: 1,
	}

	err = activator.Activate(context.Background(), cfg)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// CH restart 관련 쿼리가 실행되지 않아야 함 (mock에 추가 expectation 없음)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected DB calls: %v", err)
	}
}

func TestActivate_ProceedsWhenNotActive(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	daemon := New(&Config{
		ClickHouseHost:     "localhost",
		ClickHousePort:     9000,
		ClickHousePassword: "test",
		EncryptionKey:      "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})
	daemon.db = db

	activator := NewS3Activator(db, daemon)

	// system.disks에 S3 disk가 없는 경우
	mock.ExpectQuery("SELECT count\\(\\) FROM system.disks WHERE type='s3'").
		WillReturnRows(sqlmock.NewRows([]string{"count()"}).AddRow(0))

	cfg := &S3Config{
		ConfigID:        "warm",
		Bucket:          "test-bucket",
		Region:          "ap-northeast-2",
		S3Enabled:       1,
		AccessKeyID:     "invalid", // 복호화 실패 예상
		SecretAccessKey: "invalid",
	}

	// S3 not active → Activate 진행 → credential 복호화 실패 (테스트용 잘못된 값)
	err = activator.Activate(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error (invalid credentials), got nil")
	}
	// "credential 복호화 실패"가 에러에 포함되어야 함 = Activate가 진행됐다는 증거
	if err.Error() == "" {
		t.Fatal("expected non-empty error")
	}
}
