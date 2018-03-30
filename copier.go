package copier

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// Copy copy things
func Copy(toValue interface{}, fromValue interface{}) (err error) {
	var (
		isSlice bool
		amount  = 1
		from    = indirect(reflect.ValueOf(fromValue))
		to      = indirect(reflect.ValueOf(toValue))
	)

	if !to.CanAddr() {
		return errors.New("copy to value is unaddressable")
	}

	// Return is from value is invalid
	if !from.IsValid() {
		return
	}

	// Just set it if possible to assign
	if from.Type().AssignableTo(to.Type()) {
		to.Set(from)
		return
	}

	fromType := indirectType(from.Type())
	toType := indirectType(to.Type())

	if fromType.Kind() != reflect.Struct || toType.Kind() != reflect.Struct {
		return
	}

	if to.Kind() == reflect.Slice {
		isSlice = true
		if from.Kind() == reflect.Slice {
			amount = from.Len()
		}
	}

	for i := 0; i < amount; i++ {
		var dest, source reflect.Value

		if isSlice {
			// source
			if from.Kind() == reflect.Slice {
				source = indirect(from.Index(i))
			} else {
				source = indirect(from)
			}

			// dest
			dest = indirect(reflect.New(toType).Elem())
		} else {
			source = indirect(from)
			dest = indirect(to)
		}

		// Copy from field to field or method
		for _, field := range deepFields(fromType) {
			name := field.Name

			if fromField := source.FieldByName(name); fromField.IsValid() {
				// has field
				if toField := dest.FieldByName(name); toField.IsValid() {
					if toField.CanSet() {
						if !set(toField, fromField) {
							if err := Copy(toField.Addr().Interface(), fromField.Interface()); err != nil {
								return err
							}
						}
					}
				} else {
					// try to set to method
					var toMethod reflect.Value
					if dest.CanAddr() {
						toMethod = dest.Addr().MethodByName(name)
					} else {
						toMethod = dest.MethodByName(name)
					}

					if toMethod.IsValid() && toMethod.Type().NumIn() == 1 && fromField.Type().AssignableTo(toMethod.Type().In(0)) {
						toMethod.Call([]reflect.Value{fromField})
					}
				}
			}
		}

		// Copy from method to field
		for _, field := range deepFields(toType) {
			name := field.Name

			var fromMethod reflect.Value
			if source.CanAddr() {
				fromMethod = source.Addr().MethodByName(name)
			} else {
				fromMethod = source.MethodByName(name)
			}

			if fromMethod.IsValid() && fromMethod.Type().NumIn() == 0 && fromMethod.Type().NumOut() == 1 {
				if toField := dest.FieldByName(name); toField.IsValid() && toField.CanSet() {
					values := fromMethod.Call([]reflect.Value{})
					if len(values) >= 1 {
						set(toField, values[0])
					}
				}
			}
		}

		if isSlice {
			if dest.Addr().Type().AssignableTo(to.Type().Elem()) {
				to.Set(reflect.Append(to, dest.Addr()))
			} else if dest.Type().AssignableTo(to.Type().Elem()) {
				to.Set(reflect.Append(to, dest))
			}
		}
	}
	return
}

func deepFields(reflectType reflect.Type) []reflect.StructField {
	var fields []reflect.StructField

	if reflectType = indirectType(reflectType); reflectType.Kind() == reflect.Struct {
		for i := 0; i < reflectType.NumField(); i++ {
			v := reflectType.Field(i)
			if v.Anonymous {
				fields = append(fields, deepFields(v.Type)...)
			} else {
				fields = append(fields, v)
			}
		}
	}

	return fields
}

func indirect(reflectValue reflect.Value) reflect.Value {
	for reflectValue.Kind() == reflect.Ptr {
		reflectValue = reflectValue.Elem()
	}
	return reflectValue
}

func indirectType(reflectType reflect.Type) reflect.Type {
	for reflectType.Kind() == reflect.Ptr || reflectType.Kind() == reflect.Slice {
		reflectType = reflectType.Elem()
	}
	return reflectType
}

func set(to, from reflect.Value) bool {
	toKind := to.Kind()
	toType := to.Type()

	if from.IsValid() {
		if toKind == reflect.Ptr {
			//set `to` to nil if from is nil
			if from.Kind() == reflect.Ptr && from.IsNil() {
				to.Set(reflect.Zero(toType))
				return true
			} else if to.IsNil() {
				to.Set(reflect.New(toType.Elem()))
			}
			to = to.Elem()
		}

		var valuer driver.Valuer
		var stringer fmt.Stringer
		if from.CanAddr() {
			fromAddrIf := from.Addr().Interface()
			valuer, _ = fromAddrIf.(driver.Valuer)
			stringer, _ = fromAddrIf.(fmt.Stringer)
		}

		if from.Type().ConvertibleTo(toType) {
			to.Set(from.Convert(toType))
		} else if scanner, ok := to.Addr().Interface().(sql.Scanner); ok {
			if strings.HasSuffix(toType.PkgPath(), "nulls") {
				vstr := from.String()
				if len(vstr) < 1 {
					return true
				}
			}
			err := scanner.Scan(from.Interface())
			if err != nil {
				return false
			}
		} else if valuer != nil {
			val, err := valuer.Value()
			if err != nil {
				return false
			}
			if vstr, ok := val.(string); ok {
				if toKind == reflect.String {
					to.SetString(vstr)
				} else if toKind == reflect.Map {
					m := make(map[string]string)
					err := json.Unmarshal([]byte(vstr), &m)
					if err != nil {
						return false
					}
					to.Set(reflect.ValueOf(m))
				}
			} else {
				return false
			}
		} else if stringer != nil {
			to.SetString(stringer.String())
		} else if from.Kind() == reflect.Ptr {
			return set(to, from.Elem())
		} else {
			return false
		}
	}
	return true
}
