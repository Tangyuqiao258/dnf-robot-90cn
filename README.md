# dnf-robot-90cn

90CN(DNF 单机服务端)的**假人机器人**:一个直接讲服务端线路协议的进程内客户端,把建号、上线、配装、走位、喊话、摆摊/拍卖/邮件等行为批量自动化,用来把城镇点活。

不是外挂、不改客户端:机器人自己实现 90CN 的登录与游戏协议(`internal/protocol/cn90`),以真实角色的身份登录服务端,服务端把它当普通玩家对待。

## 能力

- **基础**:建号、登录、退出、保活、城镇移动、普通喊话、在线人数自适应调度。
- **外观与配装**:按等级表建号;装备/装扮成套发放(含宠物、称号、增幅),成套与散件比例、名称黑名单等规则全部走配置。
- **交际**:组队接受、城镇跟随、公会邀请、队伍结束恢复。
- **交易**:摆摊、副职业、分解、拍卖、金币寄售、邮件收信。
- **按需分布**:常驻人群钉在指定地图/矩形(如赫顿玛尔广场),浮动层跟着真人玩家在 ≤3 跳地图内分层铺开;不在需求内的假人分批节流下线(一批 5 个、间隔 5s)。

## 结构

```text
cmd/robot/               进程入口与装配根
internal/
  architecture/          分层的自动执法测试(依赖白名单,不可绕过)
  bootstrap/runtime/     运行目录初始化与后端切换
  entry/                 tcpapi(本机指令口)+ webadmin(内嵌 Web 控制台)
  scheduler/             调度核心:自适应策略、Actor 宿主、命令入口
  actor/                 执行模型:Actor 循环/状态机/租约
  capability/            11 个与版本无关的业务能力
  composition/backend/   适配器注册表 + cn90 适配器(唯一版本特化入口)
  protocol/cn90/         90CN 线路协议
  shared/                零依赖公共契约
  foundation/            基础设施(锁/日志/配置/布局/网络/进程…)
deploy/                  build-robot.bat 构建脚本、soak 长稳压测
doc/                     全量文档(文档只在这一处维护)
```

## 构建

```bat
deploy\build-robot.bat
```

产物输出到 `deploy\output\`:

- `robot.exe`:Windows amd64,**当前唯一允许运行的分发形态**——入口检查会刻意拒绝在非 Windows 上启动。
- `robot-linux-amd64`:Linux amd64 交叉编译产物,供 CI 校验跨平台。

要求 Go 1.26 及以上。自检:

```text
go vet ./...
go test ./... -count=1
```

推送到 `main` 会触发 `.github/workflows/release.yml`:Windows 与 Linux 两路 verify,随后打包 `latest` Release。

## 运行

首次启动按适配器能力生成运行目录(`config/`、`logs/`、`pvf/`、`templates/`、`state/` 等):

- `config/conf/config.ini`:端口与 Web 口令(`RobotAPI` 默认 8111、`Web` 默认 8112)。
- `config/conf/robot_config.ini`:建号/配装/喊话/调度/常驻与浮动层等操作配置。

长稳压测:`deploy\soak\soak.ps1 start <档位>`,状态目录与日志读法见[构建与发布](doc/构建与发布.md)。

⚠️ 仅用于可信内网或单机:指令口 8111 无鉴权,Web 默认口令 `twadmin` 且无 TLS;首次部署后立即修改口令并限制来源。

## 文档

`doc/` 是唯一的文档来源;下表只是入口。

| 文档 | 内容 |
|---|---|
| [架构设计](doc/架构设计.md) | 分层、生命周期、能力分组 |
| [代码图谱](doc/代码图谱.md) | 全库地图与阅读路线 |
| [构建与发布](doc/构建与发布.md) | 构建、发布流水线、soak 压测与日志读法 |
| [90CN适配笔记](doc/90CN适配笔记.md) | 90CN 协议/PVF 适配细节 |
| [90CN能力状态](doc/90CN能力状态.md) | 版本能力矩阵(随适配器同步更新) |
| [新增适配端](doc/新增适配端.md) | 换版本的接入方式 |
| [数据库边界](doc/数据库边界.md) | 持久化边界与表结构 |
| [状态与锁规范](doc/状态与锁规范.md) | 统一状态表与具名锁 |
| [Web交互清单](doc/Web交互清单.md) | Web 控制台功能面 |
| [优化与跟随规划](doc/优化与跟随规划.md) | 跟随与优化方向 |
| [后续工作计划](doc/后续工作计划.md) | 待办与路线 |
| [调整文档](doc/调整文档.md) · [规整文档](doc/规整文档.md) · [代码审查记录](doc/代码审查记录.md) | 变更、规整与审查记录 |
