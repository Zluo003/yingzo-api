package handler

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

// ProvideAdminHandlers 是纯装配函数：路由注册时会直接解引用它返回的每个字段，
// 任何一个字段漏接线都会在真实请求上 panic（/admin/file-service/* 就这样 500 过）。
//
// 这里按函数签名用反射造出每个参数的非 nil 指针，再断言返回结构体里没有任何指针
// 字段是 nil——漏传参数、忘了赋值、或新增 handler 却没接到结构体上都会在这里失败。
func TestProvideAdminHandlersForwardsEveryHandler(t *testing.T) {
	provide := reflect.ValueOf(ProvideAdminHandlers)
	provideType := provide.Type()

	arguments := make([]reflect.Value, provideType.NumIn())
	for i := range arguments {
		parameter := provideType.In(i)
		require.Equalf(t, reflect.Ptr, parameter.Kind(), "参数 %d 不是指针，无法用非 nil 值占位", i)
		arguments[i] = reflect.New(parameter.Elem())
	}

	results := provide.Call(arguments)
	require.Len(t, results, 1)

	handlers := results[0].Elem()
	require.Equal(t, "AdminHandlers", handlers.Type().Name())

	missing := make([]string, 0)
	for i := 0; i < handlers.NumField(); i++ {
		field := handlers.Field(i)
		if field.Kind() != reflect.Ptr {
			continue
		}
		if field.IsNil() {
			missing = append(missing, handlers.Type().Field(i).Name)
		}
	}
	require.Emptyf(t, missing, "ProvideAdminHandlers 漏接线：%v", missing)
}
