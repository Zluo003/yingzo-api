package schema

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ent schema 里声明的每个字段名最终都会变成 ent 生成代码里的列名。一旦某个字段
// 对应的物理列被迁移重命名或删除，代码却还在查旧列名，那么**所有**涉及该表的查询
// 都会失败——不是单个接口报错，而是整张表的读写全挂。
//
// 这正是 groups.models_list_config 的事故形态：迁移 235 把该列重命名为
// model_allowlist，schema 却继续声明旧字段，于是分组列表、分组创建等全部返回
// `pq: column "models_list_config" of relation "groups" does not exist`（500）。
//
// 本用例把「被迁移改过名的列不得继续出现在 group schema 中」固化成断言。
func TestGroupSchemaDoesNotDeclareRenamedAwayColumns(t *testing.T) {
	content, err := os.ReadFile("group.go")
	require.NoError(t, err)
	source := string(content)

	// 迁移 235/236：models_list_config -> model_allowlist（配置原样保留）。
	require.NotContains(t, source, `field.JSON("models_list_config"`,
		"models_list_config 已被迁移 235 重命名为 model_allowlist，group schema 不能再声明它；"+
			"否则 ent 会继续查询一个不存在的列，导致 groups 的全部接口 500")
	require.NotContains(t, source, `field.String("models_list_config"`)

	require.Contains(t, source, `field.JSON("model_allowlist"`,
		"model_allowlist 必须仍然声明：它是 groups 的模型白名单列")
}

// Group 字段名 → 物理列名，供上面的断言与人工排查参照。
// 仅覆盖本次事故涉及的列，避免维护一份会持续漂移的全量清单。
func TestGroupSchemaKeepsAllowlistColumnNamed(t *testing.T) {
	content, err := os.ReadFile("group.go")
	require.NoError(t, err)

	declared := regexp.MustCompile(`field\.(?:JSON|String|Bool|Int|Time)\("([a-z0-9_]+)"`).
		FindAllStringSubmatch(string(content), -1)
	names := make([]string, 0, len(declared))
	for _, match := range declared {
		names = append(names, match[1])
	}

	require.Contains(t, names, "model_allowlist")
	for _, name := range names {
		require.False(t, strings.HasPrefix(name, "models_list_"),
			"疑似遗留的旧列名 %q：迁移 235 之后 groups 不再有 models_list_* 列", name)
	}
}
