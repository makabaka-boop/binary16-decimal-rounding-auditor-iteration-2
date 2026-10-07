# halfconv — 精确十进制 → IEEE binary16 批处理转换器

把仪器测得的十进制字符串批次转换为 IEEE 754-2008 **binary16（半精度）**
位型。整个转换只用 Go 的整数与有理数运算（`math/big.Int` /
`math/big.Rat`），**不经过宿主浮点**，因此：

- 不会出现“先转 `double` 再强转 half”在居中点上的二次舍入；
- 极小值也不会在不可见的二进制舍入中悄悄带上非预期的符号——
  `-0` 与 `+0` 都按输入字面量原样保留为带符号零。

## 输入 / 输出

请求是 JSON，支持两种外形：

```json
{ "values": ["0.1", "-0", "65520", "1e-50"] }
```

或直接一个字符串数组：

```json
["0.1", "-0", "65520", "1e-50"]
```

约束：

- 每批 **1～1000** 个字符串；
- 每个值最多 **30 位有效数字**（前导零不计）；
- 十进制指数限于 **-50～50**（含端点）；
- 只接受普通有限十进制字面量；`NaN`、`Infinity`、`inf`、
  十六进制、下划线、空字符串等一律拒绝。

每个元素独立处理，单个非法值不会中断整批：

```json
{
  "count": 4,
  "results": [
    {
      "index": 0,
      "input": "0.1",
      "ok": true,
      "hex": "2E66",
      "class": "normal",
      "rounding_error": "-1/40960"
    },
    {
      "index": 1,
      "input": "-0",
      "ok": true,
      "hex": "8000",
      "class": "zero",
      "rounding_error": "0/1"
    },
    {
      "index": 2,
      "input": "65520",
      "ok": true,
      "hex": "7C00",
      "class": "infinity",
      "rounding_error": null
    },
    {
      "index": 3,
      "input": "NaN",
      "ok": false,
      "error": "invalid decimal format"
    }
  ]
}
```

- `hex`：4 位十六进制 binary16 位型；
- `class`：`zero` / `subnormal` / `normal` / `infinity`；
- `rounding_error`：**舍入值 − 原值** 的最简分数（`big.Rat` 天然约分）；
  结果为无穷时是 `null`；被拒绝的元素不含该字段。

## 可选：精确区间证书

复核边界值时，只看单点误差不够：请求里加 `"with_intervals": true`
（或命令行加 `-with-intervals`），每个合法结果会额外给出**恰好会编码成
该位型的全部实数输入**的区间，边界全程是 `big.Rat` 最简分数，**不使用
宿主浮点或近似小数**；未启用时输出与原来逐字段一致。

```json
{
  "hex": "2E66",
  "class": "normal",
  "rounding_error": "-1/40960",
  "interval": {
    "low": "3275/32768",
    "high": "3277/32768",
    "low_inclusive": true,
    "high_inclusive": true
  }
}
```

- 边界是与**相邻位型**的精确中点 `(v₋+v)/2`；端点归属按
  roundTiesToEven：结果尾数字段 F 为偶时两端点都含，F 为奇时两端点
  都不含（`big.Rat` 自动约分，例如次正规/正规交界写作 `2047/33554432`）；
- 区间跨 binade 时两侧 ULP 不同，中点严格按各自相邻值分别取，因此
  2 的幂（F=0）两侧边界天然不对称；
- 正负零按输入符号分开：`+0` 为 `[0, 2⁻²⁵]`，`-0` 为 `[-2⁻²⁵, 0]`，
  零值点凭输入符号裁决（`-0` 进负零证书，`0`/`+0` 进正零证书）；
- 最大有限值 65504 的区间上界为 65520（不含，F=1023 为奇）；溢出结果
  明确表示尾区间：正无穷 `[65520/1, null)`（中点 65520 归偶候选无穷），
  负无穷 `(null, -65520/1]`，`null` 端表示无穷外侧；
- 证书可脱离本程序独立裁决：把另一个合法十进制解析为有理数，按端点及
  `*_inclusive` 比较即可判定它是否编码成同一 `hex`，结论与 `hex`、
  `class`、`rounding_error` 完全一致；
- 单项非法输入依旧各自失败、不带 `interval`，不影响同批其余结果。

## 舍入规则

IEEE binary16，**roundTiesToEven**（最近值，正好居中取偶数）：

- 正规区按 binade 定位后，把尾数放大成整数，用整数商/余数比较
  `2·余数` 与分母，决定下取、上取或偶舍入，全程不出现浮点；
- 次正规区间距为 2⁻²⁴，居中点 2⁻²⁵ 在零（偶数）与最小次正规之间，
  偶舍入到零；
- 边界居中点 2⁻¹⁴·(2047/2) 在最大次正规（奇数）与最小正规（偶数）
  之间，偶舍入到最小正规；
- 最大有限值 65504 与 +∞ 的居中点 **65520** 偶舍入到无穷（无穷候选的
  尾数字段为 0，是偶数）。

## 运行

本地：

```sh
go test ./...
go build ./cmd/halfconv
./halfconv data/batch.json
cat data/batch.json | ./halfconv
./halfconv -with-intervals data/batch.json   # 附带精确区间证书
```

Docker Compose（`half` 服务负责批量运行；镜像构建阶段会跑完整测试，
包括全部 63488 个有限位型的穷举往返）：

```sh
docker compose up --build half             # 转换挂载的 /data/batch.json
docker compose run --rm half /data/my.json
cat batch.json | docker compose run --rm -T half -
```

## 目录

| 路径 | 作用 |
| --- | --- |
| `internal/decimal` | 十进制字符串 → 精确 `big.Rat`（含 30 位/指数/格式校验） |
| `internal/half` | binary16 纯整数/有理数舍入、位型重建与精确区间证书 |
| `internal/app` | JSON 批处理装配与命令行入口逻辑 |
| `cmd/halfconv` | 命令行 |
| `data/batch.json` | 示例批次（含边界值与非法值） |

## 测试

- `TestAllFinitePatternsRoundTrip`：穷举全部 **2 × 31 × 1024 = 63488**
  个有限 binary16 位型——精确位型值渲染成十进制、解析、再舍入，必须
  还原同一位型、同一分类与零误差，正负零分别覆盖；
- 次正规边界（2⁻²⁵ 居中、2⁻¹⁴ 跨界居中、最大/最小次正规）；
- 正规居中点（1+2⁻¹¹、1.5+2⁻¹¹、顶 binade 的 65488）与溢出
  （65504 / 65520 / ±1e50）；
- 正负零、`0.1`（误差精确为 −1/40960）、极小值 1e-50 的有符号零；
- 解析器对 NaN/Infinity/非法格式/超位数/超指数的拒绝；
- 另含一个**完全独立的暴力 oracle**：枚举所有 binary16 候选直接比较
  有理数距离做 tie-to-even，与生产实现在数千个二进制网格点（含大量
  精确居中点）和随机十进制串上对拍；
- 区间证书以**相邻位型的精确中点为独立预言机**：穷举全部有限位型核对
  每个端点的有理数与包含性、中点两侧各 1/8-ULP 探针、正负零分开、
  次正规/正规交界、65504 与溢出尾区间；再用数千个随机合法十进制验证
  证书构成精确划分（值只被自己的区间接受、相邻位型区间一律拒绝），
  并从 JSON 证书反解有理数独立裁决另一十进制是否编码成同一位型，
  覆盖混合批次中夹带非法值的情形。
