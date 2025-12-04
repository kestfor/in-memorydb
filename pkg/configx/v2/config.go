package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

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

	// Читаем YAML файл
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	// Парсим YAML
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("failed to parse yaml: %w", err)
	}

	// Обрабатываем переменные окружения и defaults
	if err := processStruct(v.Elem(), ""); err != nil {
		return err
	}

	return nil
}

// processStruct рекурсивно обрабатывает структуру и её вложенные поля
func processStruct(v reflect.Value, prefix string) error {
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)

		// Пропускаем неэкспортируемые поля
		if !field.CanSet() {
			continue
		}

		// Получаем теги
		yamlTag := fieldType.Tag.Get("yaml")
		envTag := fieldType.Tag.Get("env")
		defaultTag := fieldType.Tag.Get("default")
		requiredTag := fieldType.Tag.Get("required")

		// Обрабатываем вложенные структуры
		if field.Kind() == reflect.Struct {
			newPrefix := prefix
			if yamlTag != "" && yamlTag != "-" {
				// Извлекаем имя без дополнительных опций
				yamlName := strings.Split(yamlTag, ",")[0]
				if newPrefix != "" {
					newPrefix = newPrefix + "_" + strings.ToUpper(yamlName)
				} else {
					newPrefix = strings.ToUpper(yamlName)
				}
			}
			if err := processStruct(field, newPrefix); err != nil {
				return err
			}
			continue
		}

		// Проверяем env переменную (приоритет 1)
		envValue := ""
		if envTag != "" {
			envValue = os.Getenv(envTag)
		}

		// Проверяем, заполнено ли поле из YAML (приоритет 2)
		yamlFilled := !isZeroValue(field)

		// Если есть env переменная - используем её
		if envValue != "" {
			if err := setFieldValue(field, envValue); err != nil {
				return fmt.Errorf("failed to set env value for field %s: %w", fieldType.Name, err)
			}
		} else if !yamlFilled && defaultTag != "" {
			// Если YAML не заполнил поле и есть default - используем его
			if err := setFieldValue(field, defaultTag); err != nil {
				return fmt.Errorf("failed to set default value for field %s: %w", fieldType.Name, err)
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
		intVal, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		field.SetInt(intVal)
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
	default:
		return fmt.Errorf("unsupported field type: %s", field.Kind())
	}
	return nil
}
