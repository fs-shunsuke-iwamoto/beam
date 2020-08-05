// Licensed to the Apache Software Foundation (ASF) under one or more
// contributor license agreements.  See the NOTICE file distributed with
// this work for additional information regarding copyright ownership.
// The ASF licenses this file to You under the Apache License, Version 2.0
// (the "License"); you may not use this file except in compliance with
// the License.  You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package schema contains utility functions for relating Go types and Beam Schemas.
//
// Not all Go types can be converted to schemas. This is Go is more expressive than
// Beam schemas. Just as not all Go types can be serialized, similarly,
// not all Beam Schemas will have a conversion to Go types, until the correct
// mechanism exists in the SDK to handle them.
//
// While efforts will be made to have conversions be reversable, this will not
// be possible in all instances. Eg. Go arrays as fields will be converted to
// Beam Arrays, but a Beam Array type will map by default to a Go slice.
package schema

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/apache/beam/sdks/go/pkg/beam/core/util/reflectx"
	"github.com/apache/beam/sdks/go/pkg/beam/internal/errors"
	pipepb "github.com/apache/beam/sdks/go/pkg/beam/model/pipeline_v1"
)

// FromType returns a Beam Schema of the passed in type.
// Returns an error if the type cannot be converted to a Schema.
func FromType(ot reflect.Type) (*pipepb.Schema, error) {
	t := ot // keep the original type for errors.
	// The top level schema for a pointer to struct and the struct is the same.
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, errors.Errorf("cannot convert %v to schema. FromType only converts structs to schemas", ot)
	}
	return structToSchema(t), nil
}

func structToSchema(t reflect.Type) *pipepb.Schema {
	fields := make([]*pipepb.Field, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		fields = append(fields, structFieldToField(t.Field(i)))
	}
	return &pipepb.Schema{
		Fields: fields,
	}
}

func structFieldToField(sf reflect.StructField) *pipepb.Field {
	name := sf.Name
	if tag := sf.Tag.Get("beam"); tag != "" {
		name, _ = parseTag(tag)
	}
	ftype := reflectTypeToFieldType(sf.Type)

	return &pipepb.Field{
		Name: name,
		Type: ftype,
	}
}

func reflectTypeToFieldType(ot reflect.Type) *pipepb.FieldType {
	var isPtr bool
	t := ot
	if t.Kind() == reflect.Ptr {
		isPtr = true
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Map:
		kt := reflectTypeToFieldType(t.Key())
		vt := reflectTypeToFieldType(t.Elem())
		return &pipepb.FieldType{
			Nullable: isPtr,
			TypeInfo: &pipepb.FieldType_MapType{
				MapType: &pipepb.MapType{
					KeyType:   kt,
					ValueType: vt,
				},
			},
		}
	case reflect.Struct:
		sch := structToSchema(t)
		return &pipepb.FieldType{
			Nullable: isPtr,
			TypeInfo: &pipepb.FieldType_RowType{
				RowType: &pipepb.RowType{
					Schema: sch,
				},
			},
		}
	case reflect.Slice, reflect.Array:
		// Special handling for []byte
		if t == reflectx.ByteSlice {
			return &pipepb.FieldType{
				Nullable: isPtr,
				TypeInfo: &pipepb.FieldType_AtomicType{
					AtomicType: pipepb.AtomicType_BYTES,
				},
			}
		}
		vt := reflectTypeToFieldType(t.Elem())
		return &pipepb.FieldType{
			Nullable: isPtr,
			TypeInfo: &pipepb.FieldType_ArrayType{
				ArrayType: &pipepb.ArrayType{
					ElementType: vt,
				},
			},
		}
	case reflect.Interface, reflect.Chan, reflect.UnsafePointer, reflect.Complex128, reflect.Complex64, reflect.Int:
		panic(fmt.Sprintf("Unsupported type to convert to schema: %v", ot))
	default: // must be an atomic type
		enum := reflectTypeToAtomicType(t)
		return &pipepb.FieldType{
			Nullable: isPtr,
			TypeInfo: &pipepb.FieldType_AtomicType{
				AtomicType: enum,
			},
		}
	}
}

func reflectTypeToAtomicType(rt reflect.Type) pipepb.AtomicType {
	switch rt {
	case reflectx.Uint8:
		return pipepb.AtomicType_BYTE
	case reflectx.Int16:
		return pipepb.AtomicType_INT16
	case reflectx.Int32:
		return pipepb.AtomicType_INT32
	case reflectx.Int64, reflectx.Int:
		return pipepb.AtomicType_INT64
	case reflectx.Float32:
		return pipepb.AtomicType_FLOAT
	case reflectx.Float64:
		return pipepb.AtomicType_DOUBLE
	case reflectx.String:
		return pipepb.AtomicType_STRING
	case reflectx.Bool:
		return pipepb.AtomicType_BOOLEAN
	case reflectx.ByteSlice:
		return pipepb.AtomicType_BYTES
	default:
		panic(fmt.Sprintf("non atomic reflect type: %v", rt))
	}
}

// ToType returns a Go type of the passed in Schema.
// Types returned by ToType are always of Struct kind.
// Returns an error if the Schema cannot be converted to a type.
func ToType(s *pipepb.Schema) (reflect.Type, error) {
	fields := make([]reflect.StructField, 0, len(s.GetFields()))
	for _, sf := range s.GetFields() {
		rf := fieldToStructField(sf)
		fields = append(fields, rf)
	}
	return reflect.StructOf(fields), nil
}

func fieldToStructField(sf *pipepb.Field) reflect.StructField {
	name := sf.GetName()
	return reflect.StructField{
		Name: strings.ToUpper(name[:1]) + name[1:], // Go field name must be capitalized for export and encoding.
		Type: fieldTypeToReflectType(sf.GetType()),
		Tag:  reflect.StructTag(fmt.Sprintf("beam:\"%s\"", name)),
	}
}

var atomicTypeToReflectType = map[pipepb.AtomicType]reflect.Type{
	pipepb.AtomicType_BYTE:    reflectx.Uint8,
	pipepb.AtomicType_INT16:   reflectx.Int16,
	pipepb.AtomicType_INT32:   reflectx.Int32,
	pipepb.AtomicType_INT64:   reflectx.Int64,
	pipepb.AtomicType_FLOAT:   reflectx.Float32,
	pipepb.AtomicType_DOUBLE:  reflectx.Float64,
	pipepb.AtomicType_STRING:  reflectx.String,
	pipepb.AtomicType_BOOLEAN: reflectx.Bool,
	pipepb.AtomicType_BYTES:   reflectx.ByteSlice,
}

func fieldTypeToReflectType(sft *pipepb.FieldType) reflect.Type {
	var t reflect.Type
	switch sft.GetTypeInfo().(type) {
	case *pipepb.FieldType_AtomicType:
		var ok bool
		if t, ok = atomicTypeToReflectType[sft.GetAtomicType()]; !ok {
			panic(fmt.Sprintf("unknown atomic type: %v", sft.GetAtomicType()))
		}
	case *pipepb.FieldType_ArrayType:
		t = reflect.SliceOf(fieldTypeToReflectType(sft.GetArrayType().GetElementType()))
	case *pipepb.FieldType_MapType:
		kt := fieldTypeToReflectType(sft.GetMapType().GetKeyType())
		vt := fieldTypeToReflectType(sft.GetMapType().GetValueType())
		t = reflect.MapOf(kt, vt) // Panics for invalid map keys (slices/iterables)
	case *pipepb.FieldType_RowType:
		rt, err := ToType(sft.GetRowType().GetSchema())
		if err != nil {
			panic(err)
		}
		t = rt
	// case *pipepb.FieldType_IterableType:
	// TODO(BEAM-9615): handle IterableTypes.

	// case *pipepb.FieldType_LogicalType:
	// TODO(BEAM-9615): handle LogicalTypes types.

	// Logical Types are for things that have more specialized user representation already, or
	// things like Time or protocol buffers.
	// They would be encoded with the schema encoding.

	default:
		panic(fmt.Sprintf("unknown fieldtype: %T", sft.GetTypeInfo()))
	}
	if sft.GetNullable() {
		return reflect.PtrTo(t)
	}
	return t
}

// parseTag splits a struct field's beam tag into its name and
// comma-separated options.
func parseTag(tag string) (string, options) {
	if idx := strings.Index(tag, ","); idx != -1 {
		return tag[:idx], options(tag[idx+1:])
	}
	return tag, options("")
}

type options string

// TODO(BEAM-9615): implement looking up specific options from the tags.
