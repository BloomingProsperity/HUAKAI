package requesttracehttp

import "github.com/shopspring/decimal"

// addDecimal 把两个十进制字符串相加;任一无法解析按 0 处理(费用列由数据库保证格式,这里只做防御)。
func addDecimal(a, b string) string {
	x, err := decimal.NewFromString(a)
	if err != nil {
		x = decimal.Zero
	}
	y, err := decimal.NewFromString(b)
	if err != nil {
		y = decimal.Zero
	}
	return x.Add(y).String()
}
