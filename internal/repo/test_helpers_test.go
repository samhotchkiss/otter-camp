package repo

import (
	"context"
	"fmt"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type stubQuerier struct {
	queryRow func(ctx context.Context, sql string, args ...any) pgx.Row
	query    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	exec     func(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func (s stubQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if s.queryRow == nil {
		return errRow(fmt.Errorf("unexpected QueryRow: %s", sql))
	}
	return s.queryRow(ctx, sql, args...)
}

func (s stubQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if s.query == nil {
		return nil, fmt.Errorf("unexpected Query: %s", sql)
	}
	return s.query(ctx, sql, args...)
}

func (s stubQuerier) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if s.exec == nil {
		return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec: %s", sql)
	}
	return s.exec(ctx, sql, args...)
}

type stubRow struct {
	scan func(dest ...any) error
}

func (r stubRow) Scan(dest ...any) error {
	if r.scan == nil {
		return fmt.Errorf("no scan behavior configured")
	}
	return r.scan(dest...)
}

func errRow(err error) pgx.Row {
	return stubRow{
		scan: func(dest ...any) error {
			return err
		},
	}
}

func rowFromValues(values ...any) pgx.Row {
	return stubRow{
		scan: func(dest ...any) error {
			if len(dest) != len(values) {
				return fmt.Errorf("scan arg mismatch: got %d destinations, want %d", len(dest), len(values))
			}

			for i := range dest {
				dst := reflect.ValueOf(dest[i])
				if dst.Kind() != reflect.Pointer || dst.IsNil() {
					return fmt.Errorf("destination %d is not a non-nil pointer", i)
				}

				if values[i] == nil {
					dst.Elem().Set(reflect.Zero(dst.Elem().Type()))
					continue
				}

				src := reflect.ValueOf(values[i])
				if src.Type().AssignableTo(dst.Elem().Type()) {
					dst.Elem().Set(src)
					continue
				}
				if src.Type().ConvertibleTo(dst.Elem().Type()) {
					dst.Elem().Set(src.Convert(dst.Elem().Type()))
					continue
				}

				return fmt.Errorf("value %d (%T) not assignable to %s", i, values[i], dst.Elem().Type())
			}

			return nil
		},
	}
}
