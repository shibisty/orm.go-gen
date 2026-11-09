# Changelog

## Unreleased

### Added
- `orm-gen`: generates `orm.Mapper` methods (`OrmColumns`, `OrmScanTargets`, `OrmValues`,
  `OrmFill`) for the models of a package, run with `//go:generate go run orm-gen`.
  Models are the structs with `db` tags and a `TableName` method (or `-type`); embedded
  structs of the package and `orm.BaseModel`, `orm.Timestamps`, `orm.SoftDeletes` are
  followed the way the orm follows them by reflection. `-tests` reads `_test.go` files,
  `-o` names the output, `-check` fails when the output is out of date (CI).
