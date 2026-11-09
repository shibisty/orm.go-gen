# orm-gen

Generates the `orm.Mapper` methods of [orm](https://github.com/shibisty/orm.go) models,
so that the orm scans rows into them, fills them from documents and reads their values
without reflection.

Reflection is the default and needs nothing; the generated mappers are an optional speedup
for hot paths. With them, `Repository.All` over 100 rows is about 1.8× faster and
`FillSlice` (MongoDB, Redis) about 1.6× faster (see the benchmarks below).

## Installation

```json
{
    "devDependencies": {
        "orm-gen": "github:shibisty/orm.go-gen"
    }
}
```

```bash
gtr install
```

Requires orm 0.2.1 or later.

## Usage

Add a `go:generate` line to the package with the models and run `go generate`:

```go
//go:generate go run orm-gen

package models

type User struct {
    orm.BaseModel
    Name  string `db:"name"`
    Email string `db:"email"`
    orm.Timestamps
}

func (User) TableName() string { return "users" }
```

```bash
go generate ./...
```

This writes `orm_gen.go` next to the models, with four methods per model:

| Method | Used by |
|---|---|
| `OrmColumns() []string` | `SELECT *` column list; checked against the struct |
| `OrmScanTargets() []any` | SQL drivers: `Rows.Scan` targets |
| `OrmValues() map[string]any` | `Create`, `Update` |
| `OrmFill(map[string]any) error` | NoSQL drivers: `FillStruct`, `FillSlice` |

Commit the generated file. Nothing else changes: the same models work with or without it.

### What is generated

- Models are the structs with `db` tags and a `TableName` method. `-type User,Post` names
  the types instead (any struct with `db` tags).
- Columns are found exactly as the orm finds them by reflection: exported fields with a `db`
  tag other than `-`, in field order; embedded structs of the package and `orm.BaseModel`,
  `orm.Timestamps`, `orm.SoftDeletes` recursively. Unexported fields and `rel:"..."`
  relations are left out.
- Values are converted like `orm.FillStruct` does (`orm.Assign`): JSON numbers to integers,
  RFC 3339 strings to `time.Time`, values to pointer fields, named types (`type Status string`).
- An embedded struct from another package cannot be read without type-checking it, so it is
  an error; declare its fields in the model or embed a struct of the same package.
- Generic structs are not supported.

### Out-of-date mappers are caught

If a field is added and `go generate` is not run, the orm compares `OrmColumns` with the
struct on first use and returns an error (`the generated mappers of models.User are out of
date ...; run go generate`) instead of scanning into the wrong fields.

In CI, check that the generated file is current:

```bash
go run orm-gen -check
```

### Flags

| Flag | Meaning |
|---|---|
| `-type A,B` | the struct types to generate (default: every model) |
| `-o file.go` | output file (default `orm_gen.go`, or `orm_gen_test.go` with `-tests`) |
| `-tests` | read `_test.go` files too, for models declared in tests |
| `-check` | write nothing; exit 1 if the output file is missing or out of date |

The package directory is the argument (default: the current directory, as `go generate` runs it).

## Benchmarks

From the orm core (`go test -run '^$' -bench . -benchmem`), 10 columns, in-memory driver:

| Benchmark | Reflection | Generated |
|---|---|---|
| `Repository.All`, 100 rows | 46 µs, 170 allocs | 26 µs, 170 allocs |
| `Repository.Find` | 4.7 µs | 4.3 µs |
| `FillSlice`, 100 documents | 79 µs | 49 µs |
| `Repository.Create` | 6.0 µs | 5.6 µs |

Before orm 0.2.1 (uncached reflection) `Repository.All` over the same 100 rows took 192 µs with
778 allocations.

## Development

```bash
gtr install
gtr run test
```

`TestGeneratedCodeCompiles` builds the mappers of `testdata/models` against the installed orm
and runs them.

## License

MIT
