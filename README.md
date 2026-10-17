# dnf-robot-90cn

90CN(DNF 单机服务端)的**假人机器人**:一个直接讲服务端线路协议的进程内客户端,把建号、上线、配装、走位、组队、摆摊、拍卖、邮件等行为批量自动化,用来把城镇点活。机器人以真实角色身份登录,服务端把它当普通玩家对待——它不是外挂,也不修改客户端。

- 配套服务端:本机检出 `G:\90-CN\server-90`(「轻装上阵」)。
- 协议实现:`internal/protocol/cn90`(线路帧/opcode/UDP/花名册);版本特化适配层:`internal/composition/backend/cn90`。
- 分层层级、生命周期与新增适配端的约束见 [`doc/架构设计.md`](doc/架构设计.md),目录职责见 [`doc/代码图谱.md`](doc/代码图谱.md)。

## 能力概览

### 已实现并实机验证

- **账号与角色**:建号/删号/查重、花名册与选角、城镇路线与场景就绪回执;角色等级方案、装备/时装/宠物/神器发放。
- **城镇活动**:op35/op36 走位、跨城镇传送、按城镇/区域分布与清场。
- **经济与社交(自动化回合)**:副职业摆摊(分解机/附魔)、拍卖行买卖、邮箱领取、表情、同区域定向私聊自动回复、玩家间物品交易自动接受与出价。
- **组队**:自动接受邀请(op10/op11),城镇内跟随队长(2026-10-17 修复跟随目标 `0/0` 根因并实机验证)。
- **副本**:队长侧组队进副本(op15/op16/op37)与退出(op42);2026-10-17 起支持**副本真刷图**(按 op29 actor 行报死、boss 死检、结算 worker、DFS 换房),3 台试点两轮 `CN90_DUNGEON_CLEARED` 实机成功。
- **规模运行**:300 台机队长稳;2026-10-17 11:00–11:56 全队副本窗口 `started=128 / cleared=127 / party_exit=264 / failed=0`(此后个别失败单发,未复现)。

### 服务端没有机制或本仓不做的能力

- 世界/区域广播、服务器公告、公会邀请:90CN 服务端没有对应链路,描述符保持关闭。
- 副本内跟随、副本房间路径规划(仅按邻格探测):`CapabilityDungeonFollow` / `CapabilityDungeonMove` 保持关闭。
- 战斗系统:副本「刷图」通过协议申报击杀与结算完成,不做真实战斗模拟。

能力矩阵与每条能力的验证状态(含日期与证据)以 [`doc/90CN能力状态.md`](doc/90CN能力状态.md) 为准;增删能力时必须与 `internal/composition/backend/cn90/descriptor.go` 同步。

## 构建

要求 **Go 1.26.6 或更新**(`go.mod` 的 go 指令为 1.26.0):

```text
go test ./...
go vet ./...
deploy\build-robot.bat
```

产物输出到 `deploy\output\`,不进入仓库。当前只发布 Windows amd64 运行包;Linux 仅保留交叉编译与源码兼容。细节见 [`doc/构建与发布.md`](doc/构建与发布.md)。

## 运行

- 运行目录默认为 `G:\90-CN\robot-run`(可执行文件 + `config/`)。
- 配置:`config\conf\robot_config.ini`;日志:`config\logs\robot.log`。
- 长稳压测统一入口:

```powershell
powershell -NoProfile -ExecutionPolicy RemoteSigned -File deploy\soak\soak.ps1 start 300
powershell -NoProfile -File deploy\soak\soak.ps1 status
powershell -NoProfile -File deploy\soak\soak.ps1 stop
```

`[RobotMetrics]` 行里只有 `runtime running=<n>` 才是在线人数;`online=<n>` 与 `online=<a>/<b>` 都不是。理由见 [`doc/构建与发布.md`](doc/构建与发布.md) 的「读日志」一节。

## 仓库结构

```text
cmd/robot/               进程入口与装配
internal/capability/     与版本无关的业务能力编排(11 个子包)
internal/composition/    适配器注册表 + cn90 适配器(版本特化总入口)
internal/protocol/cn90/  90CN 线路协议
internal/shared/         零依赖公共契约
internal/foundation/     基础设施(锁/日志/配置/网络/进程)
deploy/                  构建、发布、压测脚本
doc/                     全量文档
```

## 文档

阅读入口:[`doc/规整文档.md`](doc/规整文档.md)(全部文档索引与阅读顺序)。常用:

- [`doc/90CN能力状态.md`](doc/90CN能力状态.md):能力矩阵与实机验证记录
- [`doc/调整文档.md`](doc/调整文档.md):配置键、启动修复行为、日志关键字
- [`doc/后续工作计划.md`](doc/后续工作计划.md):未完成项与下一步
- [`doc/方向与任务清单.md`](doc/方向与任务清单.md):任务分级(P0–P4)与纯机器人可行性约束

## 安全前提

Robot 只设计用于可信内网或单机环境:

- TCP 指令接口(默认 8111)监听 `0.0.0.0` 且不鉴权,必须用防火墙限制来源。
- Web 管理(默认 8112)默认口令为 `twadmin` 且无 TLS,首次部署后立即修改。

详见 [`doc/构建与发布.md`](doc/构建与发布.md) 的「安全部署前提」。

## 执行约束

分层、生命周期、锁、状态表、数据库边界等硬约束见 [`AGENTS.md`](AGENTS.md)。文档规矩:根目录只允许这一份 `README.md`,子目录不得再放 README,其余内容集中进 `doc/`(由架构测试 `TestReadmeFilesStayAtRepositoryRoot` 执法)。
