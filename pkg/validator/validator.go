package validator

import (
	"fmt"
	"regexp"
	"strings"

	v10 "github.com/go-playground/validator/v10"
)

var validate *v10.Validate

func init() {
	validate = v10.New()
	validate.RegisterValidation("account_number", validateAccountNumber)
	validate.RegisterValidation("phone", validatePhone)
	validate.RegisterValidation("bank_code", validateBankCode)
	validate.RegisterValidation("currency", validateCurrency)
}

type Errors struct {
	Fields map[string]string `json:"fields"`
}

func (e *Errors) Error() string {
	var sb strings.Builder
	for field, msg := range e.Fields {
		sb.WriteString(field)
		sb.WriteString(": ")
		sb.WriteString(msg)
		sb.WriteString("; ")
	}
	return sb.String()
}

func Struct(s interface{}) *Errors {
	err := validate.Struct(s)
	if err == nil {
		return nil
	}

	fields := make(map[string]string)
	for _, fe := range err.(v10.ValidationErrors) {
		field := toSnake(fe.Field())
		fields[field] = formatError(fe)
	}

	if len(fields) == 0 {
		return nil
	}
	return &Errors{Fields: fields}
}

func formatError(fe v10.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "this field is required"
	case "min":
		return fmt.Sprintf("must be at least %s characters", fe.Param())
	case "max":
		return fmt.Sprintf("must be at most %s characters", fe.Param())
	case "len":
		return fmt.Sprintf("must be exactly %s characters", fe.Param())
	case "email":
		return "must be a valid email address"
	case "account_number":
		return "must be a valid 10-digit account number"
	case "phone":
		return "must be a valid phone number"
	case "bank_code":
		return "must be a valid 3-digit bank code"
	case "currency":
		return "must be a valid currency code (e.g., NGN)"
	case "gte":
		return fmt.Sprintf("must be greater than or equal to %s", fe.Param())
	case "lte":
		return fmt.Sprintf("must be less than or equal to %s", fe.Param())
	case "gt":
		return "must be greater than 0"
	default:
		return fmt.Sprintf("failed validation: %s", fe.Tag())
	}
}

var (
	reAccountNumber = regexp.MustCompile(`^\d{10}$`)
	rePhone         = regexp.MustCompile(`^(\+?234)\d{10}$`)
	reBankCode      = regexp.MustCompile(`^\d{3}$`)
	reCurrency      = regexp.MustCompile(`^[A-Z]{3}$`)
)

func validateAccountNumber(fl v10.FieldLevel) bool {
	return reAccountNumber.MatchString(fl.Field().String())
}

func validatePhone(fl v10.FieldLevel) bool {
	return rePhone.MatchString(fl.Field().String())
}

func validateBankCode(fl v10.FieldLevel) bool {
	return reBankCode.MatchString(fl.Field().String())
}

func validateCurrency(fl v10.FieldLevel) bool {
	return reCurrency.MatchString(fl.Field().String())
}

func toSnake(s string) string {
	var result strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				result.WriteRune('_')
			}
			result.WriteRune(r + 32)
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}
