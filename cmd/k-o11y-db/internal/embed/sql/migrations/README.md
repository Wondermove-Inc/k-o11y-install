# DDL 마이그레이션

`upgrade` 서브커맨드에서 순서대로 실행되는 스키마 변경 파일입니다.

## 파일명 규칙

```
{번호}_{버전}_{설명}.sql
```

- **번호**: 4자리 (0001, 0002, ...) — 실행 순서 결정
- **버전**: 이 변경이 도입된 릴리즈 버전
- **설명**: 변경 내용 요약 (snake_case)

예시:
```
0001_v26.2.1_add_activate_requested.sql
0002_v26.3.0_add_new_metric_table.sql
0003_v26.3.1_add_backup_schedule_column.sql
```

## 실행 방식

- `upgrade --clickhouse-password` 실행 시 번호순으로 전체 실행
- 이미 적용된 변경은 `IF NOT EXISTS`로 자동 스킵 (멱등)
- 어떤 버전에서 업그레이드해도 0001부터 끝까지 순서대로 실행

## 안전 규칙

- **허용**: `ALTER TABLE ADD COLUMN IF NOT EXISTS`, `CREATE TABLE IF NOT EXISTS`
- **금지**: `DROP`, `MODIFY`, `RENAME` — 수동 가이드로 안내
- 각 파일 상단에 주석으로 목적과 적용 대상 버전 명시
