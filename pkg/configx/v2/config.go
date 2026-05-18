package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Load загружает конфигурацию из YAML файла и переменных окружения
// Приоритет: env > yaml > default
func Load(filePath string, cfg interface{}) error {
	// Проверяем, что передали указатель на структуру
	v := reflect.ValueOf(cfg)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("config must be a pointer to struct")
	}

	// 1. Сначала устанавливаем все defaults
	if err := applyDefaults(v.Elem()); err != nil {
		return err
	}

	// 2. Читаем и парсим YAML (перезаписывает defaults)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("failed to parse yaml: %w", err)
	}

	// 3. Применяем env переменные (перезаписывают YAML) и проверяем required
	if err := applyEnvAndValidate(v.Elem(), ""); err != nil {
		return err
	}

	return nil
}

// applyDefaults рекурсивно устанавливает default значения из тегов
func applyDefaults(v reflect.Value) error {
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		if !field.CanSet() {
			continue
		}

		// Обрабатываем вложенные структуры
		if field.Kind() == reflect.Struct {
			if err := applyDefaults(field); err != nil {
				return err
			}
			continue
		}

		// Устанавливаем default если поле zero value
		defaultTag := fieldType.Tag.Get("default")
		if defaultTag != "" && isZeroValue(field) {
			if err := setFieldValue(field, defaultTag); err != nil {
				return fmt.Errorf("failed to set default value for field %s: %w", fieldType.Name, err)
			}
		}
	}

	return nil
}

// applyEnvAndValidate применяет env переменные и проверяет required поля
func applyEnvAndValidate(v reflect.Value, prefix string) error {
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		if !field.CanSet() {
			continue
		}

		yamlTag := fieldType.Tag.Get("yaml")
		envTag := fieldType.Tag.Get("env")
		requiredTag := fieldType.Tag.Get("required")

		// Обрабатываем вложенные структуры
		if field.Kind() == reflect.Struct {
			newPrefix := prefix
			if yamlTag != "" && yamlTag != "-" {
				yamlName := strings.Split(yamlTag, ",")[0]
				if newPrefix != "" {
					newPrefix = newPrefix + "_" + strings.ToUpper(yamlName)
				} else {
					newPrefix = strings.ToUpper(yamlName)
				}
			}
			if err := applyEnvAndValidate(field, newPrefix); err != nil {
				return err
			}
			continue
		}

		// Применяем env переменную (наивысший приоритет)
		if envTag != "" {
			if envValue := os.Getenv(envTag); envValue != "" {
				if err := setFieldValue(field, envValue); err != nil {
					return fmt.Errorf("failed to set env value for field %s: %w", fieldType.Name, err)
				}
			}
		}

		// Проверяем required
		if requiredTag == "true" && isZeroValue(field) {
			fieldName := fieldType.Name
			if envTag != "" {
				fieldName = envTag
			}
			return fmt.Errorf("required field %s is not set", fieldName)
		}
	}

	return nil
}

// isZeroValue проверяет, является ли значение нулевым
func isZeroValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return v.String() == ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Slice, reflect.Map:
		return v.IsNil() || v.Len() == 0
	default:
		return false
	}
}

// setFieldValue устанавливает значение поля из строки
func setFieldValue(field reflect.Value, value string) error {
	switch field.Kind() {
	case reflect.String:
		field.SetString(value)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if field.Type() == reflect.TypeOf(time.Duration(0)) {
			d, err := time.ParseDuration(value)
			if err != nil {
				return err
			}
			field.SetInt(int64(d))
		} else {
			intVal, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return err
			}
			field.SetInt(intVal)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		uintVal, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return err
		}
		field.SetUint(uintVal)
	case reflect.Float32, reflect.Float64:
		floatVal, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return err
		}
		field.SetFloat(floatVal)
	case reflect.Bool:
		boolVal, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		field.SetBool(boolVal)
	case reflect.Slice:
		parts := strings.Split(value, ",")
		elemType := field.Type().Elem()
		slice := reflect.MakeSlice(field.Type(), 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			elem := reflect.New(elemType).Elem()
			if err := setFieldValue(elem, part); err != nil {
				return fmt.Errorf("failed to parse slice element %q: %w", part, err)
			}
			slice = reflect.Append(slice, elem)
		}
		field.Set(slice)
	default:
		return fmt.Errorf("unsupported field type: %s", field.Kind())
	}
	return nil
}
