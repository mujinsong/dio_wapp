# 微信登录与身份识别

## 设计原则

微信登录只负责识别“这个人是谁”，不直接决定业务权限。

当前业务是给事务所使用，一个事务所下面可能有多个团体，所以权限不能只做成全局 `staff`。

身份拆成这些表：

```text
users           所有登录过的小程序用户
agencies        事务所
idol_groups     团体，归属于某个事务所
staff_members   工作人员身份，必须绑定 agency_id + group_id
admin_members   管理员身份，必须绑定 agency_id
agency_point_accounts 用户在事务所内的积分账户
```

关键边界：

- 普通用户身份仍然是全局微信用户。
- 管理员是某个事务所的管理员。
- 工作人员是某个事务所下某个团体的工作人员。
- 团队负责人也是 `staff_members`，通过 `role=team_leader` 标识，仍然只作用于所绑定的团体。
- 团体 A 的工作人员不能自动操作团体 B。
- 项目只允许一个事务所，同一事务所下的所有团体共用积分。
- 后续扫码加积分、核销、商品管理，都要带上并校验 `agency_id` / `group_id`。

## 登录流程

```text
小程序 wx.login
-> POST /api/v1/auth/wechat-login
-> 后端换取 openid
-> 查找或创建 users
-> 检查 users.status
-> 查询 admin_members / staff_members 的作用域
-> 返回 JWT、用户信息、角色和身份作用域
```

默认登录方式是真实微信登录：

```text
wx.login()
-> code
-> 后端 code2Session
-> openid
```

本地开发仍然支持 mock code：

```json
{
  "code": "mock:openid_001"
}
```

mock 登录只有在 `APP_ENV=local` 且 `MOCK_LOGIN_ENABLED=true` 时可用。真实微信登录需要后端配置 `WECHAT_APPID` 和 `WECHAT_SECRET`，小程序端配置相同的 AppID。

如果 `openid` 命中 `ADMIN_OPENIDS`，后端会自动创建 `DEFAULT_AGENCY_NAME` 对应的事务所，并把该用户设为这个事务所的管理员。

如果 `openid` 首次命中 `STAFF_OPENIDS` 或 `TEAM_LEADER_OPENIDS`，后端会把用户绑定到默认团体；后者会将成员角色设为团队负责人。配置只负责首次创建成员关系，之后角色和启停状态以管理员人员管理中的数据库记录为准，后续登录不会恢复已经撤销的权限。

## API

### POST /api/v1/auth/wechat-login

请求：

```json
{
  "code": "wx.login 返回的临时 code"
}
```

普通用户返回：

```json
{
  "token": "jwt",
  "expires_in": 604800,
  "user": {
    "id": 1,
    "openid": "openid_001",
    "nickname": "",
    "avatar": "",
    "status": "normal",
    "points_balance": 0,
    "point_accounts": [],
    "roles": ["user"],
    "staff_groups": [],
    "admin_agencies": []
  }
}
```

事务所管理员返回示例：

```json
{
  "token": "jwt",
  "expires_in": 604800,
  "user": {
    "id": 2,
    "openid": "mock_admin_openid",
    "nickname": "",
    "avatar": "",
    "status": "normal",
    "points_balance": 0,
    "point_accounts": [],
    "roles": ["user", "admin"],
    "staff_groups": [],
    "admin_agencies": [
      {
        "agency_id": 1,
        "agency_name": "默认事务所",
        "display_name": "事务所管理员"
      }
    ]
  }
}
```

工作人员返回示例：

```json
{
  "token": "jwt",
  "expires_in": 604800,
  "user": {
    "id": 3,
    "openid": "staff_openid",
    "nickname": "",
    "avatar": "",
    "status": "normal",
    "points_balance": 0,
    "point_accounts": [],
    "roles": ["user", "staff"],
    "staff_groups": [
      {
        "agency_id": 1,
        "agency_name": "默认事务所",
        "group_id": 10,
        "group_name": "团体 A",
        "display_name": "现场 Staff",
        "member_role": "staff"
      }
    ],
    "admin_agencies": []
  }
}
```

团队负责人会同时返回 `staff` 和 `team_leader` 角色，所属团队的 `member_role` 为 `team_leader`。负责人继承扫码加积分、订单核销和商品查询能力；其商品变更会直接生效并保留自动批准记录，也可以批准或驳回本团队其他工作人员的待审批申请。

### GET /api/v1/auth/me

请求头：

```text
Authorization: Bearer <token>
```

返回当前用户资料，结构同登录接口里的 `user`。

## 错误格式

```json
{
  "error": {
    "code": "invalid_token",
    "message": "invalid token"
  }
}
```

## 当前数据表

`users`

```text
id
open_id
union_id
nickname
avatar
phone
points_balance
status
last_login_at
created_at
updated_at
```

`agency_point_accounts`

```text
id
agency_id
user_id
balance
created_at
updated_at
```

数据库不使用外键；事务所表通过单例唯一键禁止第二条记录，其他关系由唯一索引和服务层事务校验。

`agencies`

```text
id
name
status
remark
created_at
updated_at
```

`idol_groups`

```text
id
agency_id
name
status
remark
created_at
updated_at
```

`staff_members`

```text
id
agency_id
group_id
user_id
role
status
display_name
remark
created_by
created_at
updated_at
```

`admin_members`

```text
id
agency_id
user_id
status
display_name
remark
created_by
created_at
updated_at
```
