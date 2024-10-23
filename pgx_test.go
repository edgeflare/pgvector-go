package pgvector_test

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	pgxvector "github.com/pgvector/pgvector-go/pgx"
)

type PgxItem struct {
	Id              int64
	Embedding       pgvector.Vector
	HalfEmbedding   pgvector.HalfVector
	BinaryEmbedding string
	SparseEmbedding pgvector.SparseVector
}

func CreatePgxItems(ctx context.Context, conn *pgx.Conn) {
	items := []PgxItem{
		PgxItem{Embedding: pgvector.NewVector([]float32{1, 1, 1}), HalfEmbedding: pgvector.NewHalfVector([]float32{1, 1, 1}), BinaryEmbedding: "000", SparseEmbedding: pgvector.NewSparseVector([]float32{1, 1, 1})},
		PgxItem{Embedding: pgvector.NewVector([]float32{2, 2, 2}), HalfEmbedding: pgvector.NewHalfVector([]float32{2, 2, 2}), BinaryEmbedding: "101", SparseEmbedding: pgvector.NewSparseVector([]float32{2, 2, 2})},
		PgxItem{Embedding: pgvector.NewVector([]float32{1, 1, 2}), HalfEmbedding: pgvector.NewHalfVector([]float32{1, 1, 2}), BinaryEmbedding: "111", SparseEmbedding: pgvector.NewSparseVector([]float32{1, 1, 2})},
	}

	for _, item := range items {
		_, err := conn.Exec(ctx, "INSERT INTO pgx_items (embedding, half_embedding, binary_embedding, sparse_embedding) VALUES ($1, $2, $3, $4)", item.Embedding, item.HalfEmbedding, item.BinaryEmbedding, item.SparseEmbedding)
		if err != nil {
			panic(err)
		}
	}
}

func TestPgx(t *testing.T) {
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, "postgres://localhost/pgvector_go_test")
	if err != nil {
		panic(err)
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector")
	if err != nil {
		panic(err)
	}

	err = pgxvector.RegisterTypes(ctx, conn)
	if err != nil {
		panic(err)
	}

	_, err = conn.Exec(ctx, "DROP TABLE IF EXISTS pgx_items")
	if err != nil {
		panic(err)
	}

	_, err = conn.Exec(ctx, "CREATE TABLE pgx_items (id bigserial PRIMARY KEY, embedding vector(3), half_embedding halfvec(3), binary_embedding bit(3), sparse_embedding sparsevec(3))")
	if err != nil {
		panic(err)
	}

	_, err = conn.Exec(ctx, "CREATE INDEX ON pgx_items USING hnsw (embedding vector_l2_ops)")
	if err != nil {
		panic(err)
	}

	CreatePgxItems(ctx, conn)

	rows, err := conn.Query(ctx, "SELECT id, embedding, half_embedding, binary_embedding, sparse_embedding, embedding <-> $1 FROM pgx_items ORDER BY embedding <-> $1 LIMIT 5", pgvector.NewVector([]float32{1, 1, 1}))
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	var items []PgxItem
	var binaryEmbeddings []pgtype.Bits
	var distances []float64
	for rows.Next() {
		var item PgxItem
		var binaryEmbedding pgtype.Bits
		var distance float64
		err = rows.Scan(&item.Id, &item.Embedding, &item.HalfEmbedding, &binaryEmbedding, &item.SparseEmbedding, &distance)
		if err != nil {
			panic(err)
		}
		items = append(items, item)
		binaryEmbeddings = append(binaryEmbeddings, binaryEmbedding)
		distances = append(distances, distance)
	}

	if rows.Err() != nil {
		panic(rows.Err())
	}

	if items[0].Id != 1 || items[1].Id != 3 || items[2].Id != 2 {
		t.Error()
	}
	if !reflect.DeepEqual(items[1].Embedding.Slice(), []float32{1, 1, 2}) {
		t.Error()
	}
	if !reflect.DeepEqual(items[1].HalfEmbedding.Slice(), []float32{1, 1, 2}) {
		t.Error()
	}
	if binaryEmbeddings[1].Bytes[0] != (7<<5) || binaryEmbeddings[1].Len != 3 {
		t.Error()
	}
	if !reflect.DeepEqual(items[1].SparseEmbedding.Slice(), []float32{1, 1, 2}) {
		t.Error()
	}
	if distances[0] != 0 || distances[1] != 1 || distances[2] != math.Sqrt(3) {
		t.Error()
	}

	var item PgxItem
	row := conn.QueryRow(ctx, "SELECT embedding, half_embedding, binary_embedding, sparse_embedding FROM pgx_items ORDER BY id DESC LIMIT 1", pgx.QueryResultFormats{pgx.TextFormatCode, pgx.TextFormatCode, pgx.TextFormatCode, pgx.TextFormatCode})
	err = row.Scan(&item.Embedding, &item.HalfEmbedding, &item.BinaryEmbedding, &item.SparseEmbedding)
	if err != nil {
		panic(err)
	}
	if !reflect.DeepEqual(item.Embedding.Slice(), []float32{1, 1, 2}) {
		t.Error()
	}
	if !reflect.DeepEqual(item.HalfEmbedding.Slice(), []float32{1, 1, 2}) {
		t.Error()
	}
	if item.BinaryEmbedding != "111" {
		t.Error()
	}
	if !reflect.DeepEqual(item.SparseEmbedding.Slice(), []float32{1, 1, 2}) {
		t.Error()
	}

	_, err = conn.CopyFrom(
		ctx,
		pgx.Identifier{"pgx_items"},
		[]string{"embedding", "binary_embedding", "sparse_embedding"},
		pgx.CopyFromSlice(1, func(i int) ([]any, error) {
			return []interface{}{"[1,2,3]", "101", "{1:1,2:2,3:3}/3"}, nil
		}),
	)
	if err != nil {
		panic(err)
	}

	config, err := pgxpool.ParseConfig("postgres://localhost/pgvector_go_test")
	if err != nil {
		panic(err)
	}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return pgxvector.RegisterTypes(ctx, conn)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	defer pool.Close()

	_, err = pool.CopyFrom(
		ctx,
		pgx.Identifier{"pgx_items"},
		[]string{"embedding", "binary_embedding", "sparse_embedding"},
		pgx.CopyFromSlice(1, func(i int) ([]any, error) {
			return []interface{}{"[1,2,3]", "101", "{1:1,2:2,3:3}/3"}, nil
		}),
	)
	if err != nil {
		panic(err)
	}
}
