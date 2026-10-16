# Web 交互清单（90CN 适配端）

本文档逐个列出假人管理 Web 的按钮、输入项、弹窗、提示与错误文案，作为阶段三「Web 全量文案与语义治理」的对照基线。名称栏中的键对应 `internal/entry/webadmin/assets/i18n.js`，实现位于 `assets/index.html`、`assets/app.js`、`assets/login.html`。

## 通用规则

- 语言：中文/English 由右上角语言按钮切换，选择保存在 `localStorage.tw_language`；所有 `data-i18n` 与 `i18nFormat` 文案都有中英两份，测试 `TestI18nLocalesHaveMatchingKeys` 保证键集合一致。
- 名称长度：按钮与表单标签都为短词（中文 2–5 字，英文 ≤ 16 字符），弹窗表单标签列已加宽并使用 `white-space:nowrap`，避免换行或与输入框重叠。
- 禁用语义：能力未启用、所选后端不支持、游戏端口不可达或正在执行其他操作时，按钮禁用；能力未启用时 `title` 显示后端给出的原因（缺失时回退 `common.unavailable`）。
- 操作反馈：所有操作通过 `guarded()` 输出成功/失败 toast；批量操作额外输出计数摘要（清理、危险删除）。
- 危险级别：`普通` 仅查询；`变更` 修改配置或在线状态；`高` 停止进程或删除数据，需确认。

## 顶部栏

| 名称 | 键 | 作用 | 前置条件 | 危险 | 反馈 | API |
| --- | --- | --- | --- | --- | --- | --- |
| 服务端（显示 `display_name`） | `backend.title` | 打开服务端设置弹窗（服务端目录、游戏地址、端口、数据库覆盖） | 无 | 变更（应用后重建运行周期） | 应用后 toast「正在应用设置并重建运行周期...」 | `GET/POST /api/backend` |
| 统计 | `header.statistics` | 打开只读人口报告（性别/职业/装备/装扮/区域） | 后端数据库能力可用 | 普通 | 弹窗展示 | `populationReport` |
| 删除 | `header.purge` | 解锁后执行按规则彻底删除（CID / UID / 范围） | `dangerous_delete` 能力启用 | 高 | 解锁→填写→执行；toast 汇总已删除账号/角色/登记数 | `dangerousDeleteUnlock`、`dangerousDeleteAsync` |
| 停止 | `header.stop` | 停止 Robot 进程并保存数据 | 无 | 高 | 确认弹窗；toast「Robot 正在保存数据并退出...」 | `POST /api/stop-robot` |
| 中文 / English | `language.switch` | 切换界面语言并刷新 | 无 | 普通 | 立即生效 | 无 |
| 退出 | `header.logout` | 注销 Web 会话 | 已登录 | 普通 | 跳转登录页 | `POST /logout` |

## 概览卡片

| 名称 | 键 | 说明 |
| --- | --- | --- |
| Robot | `card.robot` | CPU 百分比、内存 MB、协程数（`dashboard.robot_threads`） |
| Game port | `card.game_port` | 游戏端口连通（`common.open`/`common.closed`），tooltip 显示地址或错误 |
| Database | `card.database` | 引擎与探测延迟；tooltip 显示目标路径、`database.writable` 或错误；能力关闭时显示 `N/A` 与原因 |
| Auto | `card.auto` | 自动调度开/关 |
| Online / Target | `card.online_target` | 运行中 / 目标在线数 |
| 运行时间 | `dashboard.runtime` | 顶部运行时长 |

## 工具栏

| 名称 | 键 | 作用 | 前置条件 | 危险 | 反馈 | API |
| --- | --- | --- | --- | --- | --- | --- |
| 刷新 | `action.refresh` | 刷新概览、列表、游戏端口 | 无 | 普通 | toast 成功/失败 | `dashboardStatus`、`robotsStatus`、`GET /api/game-port` |
| 自动 | `action.auto` | 打开自动设置弹窗 | 无 | 变更 | 保存/保存并启动后 toast；应用后重建运行周期 | `robotConfigGet`、`robotConfigUpdate`、`autoStart`、`autoStop` |
| 上线 | `action.online` | 选中假人登录并保活 | 已选假人；`provision` 能力 | 变更 | toast + 列表刷新 | `robotsOnlineAsync` |
| 移动 | `action.move` | 选中假人城镇移动 | 已选假人；`town_move` 能力；端口可达 | 变更 | toast | `robotsMove` |
| 喊话 | `action.shout` | 选中假人喊话 | 已选假人；`shout`（或 `world_shout`）能力；端口可达 | 变更 | toast | `robotsShout` |
| 下线 | `action.logout` | 选中假人退出登录 | 已选假人；端口可达 | 变更 | toast | `robotsLogoutAsync` |
| 清理 | `action.cleanup` | 通过游戏协议删除选中假人的角色 | 已选假人；`cleanup` 能力 | 高 | 确认弹窗 + 汇总 toast（请求/确认/失败） | `cleanupRobotsAsync` |
| 自动刷新 | `toolbar.auto_refresh` | 3s/5s/8s/关闭，保存到 `localStorage.tw_auto_refresh_interval` | 无 | 普通 | 立即生效 | 无 |
| 已选择 | `toolbar.selected` | 显示已选数量 | 无 | 普通 | 实时更新 | 无 |

## 调度栏（只读诊断）

| 名称 | 键 | 说明 |
| --- | --- | --- |
| 策略模式 | `scheduler.policy_mode` | 调度策略状态 |
| 连接速度 | `scheduler.attach_pace` | 每秒上线数与批大小 |
| 伸缩窗口 | `scheduler.scale_window` | 本周期扩缩容上下限 |
| 压力保护 | `scheduler.pressure_guard` | 登录中数量与 CPU 百分比 |
| 释放保护 | `scheduler.release_guard` | 熔断与端口释放批量 |
| 原因行 | `scheduler.reason_value` | 最近调度原因、运行/目标、空闲、端口与熔断状态、执行器计数、最近操作 |

## 假人表格

| 控件 | 键 | 说明 |
| --- | --- | --- |
| 全选 | 无 | 勾选当前列表全部假人；再点取消 |
| 行选择 | 无 | 点击行切换勾选（复选框除外） |
| UID / CID / X / Y | 无 | 技术标识，不翻译 |
| 名称 | `robots.name` | 角色名 |
| 演员 | `robots.actor` | 空闲（`status.free`）或槽位/状态；清理中显示 `status.deleting` |
| 状态 | `robots.state` | 实际→目标状态；tooltip 显示数据库状态、阶段、错误与操作 |
| 等级 / 职业 / 城镇 / 区域 | `robots.level`、`robots.job`、`robots.town`、`robots.area` | 职业名含觉醒档位（`awakening.1/2`） |
| 在线时间 | `robots.uptime` | `时:分:秒` |
| 商店 | `robots.store` | `status.disjoint`（分解）、`status.item`（物品摆摊）、`status.pending`（创建中） |
| 列表信息 | `robots.list_info` | 显示/总数与刷新时间 |

## 弹窗

| 弹窗 | 标题键 | 内容 | 危险 | 确认方式 |
| --- | --- | --- | --- | --- |
| 服务端设置 | `backend.title` | 服务端目录（必填）、游戏地址（必填）、端口、数据库覆盖（可选）；提示当前周期结束后生效 | 变更 | 必填校验，缺项聚焦提示；应用按钮 |
| 自动设置 | `action.auto` | 启用、邮件通知、定时系统公告、目标在线数、喊话间隔、跟随账号、固定出生/城镇/区域；保存 / 保存并启动 | 变更 | 数值范围校验（喊话间隔 1–86400） |
| 清理 | `action.cleanup` | 说明通过游戏协议删除已选假人；正常缩容不要使用 | 高 | 确认按钮 |
| 删除解锁 | `purge.unlock_title` | 风险说明 + 输入 123 | 高 | 输入 123，回车提交 |
| 数据删除 | `purge.data_title` | 模式（范围/UID/CID）、对应输入框、删除范围说明 | 高 | 单次不可撤销；执行按钮 |
| 统计 | `header.statistics` | 人口报告表格 | 普通 | 关闭 |

## 登录页

| 名称 | 键 | 说明 |
| --- | --- | --- |
| 标题 | `login.title` | 假人管理 |
| 语言 | `language.switch` | 切换语言 |
| 密码 | `login.password` | 输入 Web 密码 |
| 登录 | `login.submit` | 提交登录 |
| 错误 | `login.password_error` / `login.password_missing` | 密码错误 / 未配置密码（经 `i18nText` 翻译） |
| 恢复提示 | `login.recovery` | 需要先配置服务端 |

## 与 TCP API 的一致性

- 每个按钮的 API 名称与 `internal/entry/tcpapi` 路由一致；能力门禁与 `descriptor.go` 的 `Capabilities` 一致，测试 `TestCommandCapabilitiesCoverBackendSpecificActions` 覆盖。
- 后端不支持的接口返回明确 unsupported；Web 侧按能力禁用并显示原因（移动、喊话、清理、删除），未在界面暴露的接口（如 `robotsStore`）只能通过 API 调用，且同样受能力门禁约束。
- 危险删除的二次确认由「解锁（123）」+「删除」两步组成；删除范围只包含机器人账号（`前缀+UID`），不会触碰其他账号。
