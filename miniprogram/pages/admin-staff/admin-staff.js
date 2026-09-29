const auth = require('../../utils/auth')
const adminGoods = require('../../utils/adminGoods')
const adminStaff = require('../../utils/adminStaff')

const roleOptions = [
  { label: '工作人员', value: 'staff' },
  { label: '团队负责人', value: 'team_leader' }
]

function emptyForm() {
  return {
    user_id: '',
    display_name: '',
    remark: ''
  }
}

function presentMembers(items) {
  return items.map(item => Object.assign({}, item, {
    name_text: item.display_name || item.nickname || `会员 #${item.user_id}`,
    initial: (item.display_name || item.nickname || '员').slice(0, 1),
    account_text: `会员 #${item.user_id} · ${item.openid_hint || '微信账号'}`,
    role_text: item.role === 'team_leader' ? '团队负责人' : '工作人员',
    role_class: item.role === 'team_leader' ? 'member-role leader' : 'member-role staff',
    status_text: item.status === 'normal' ? '在职' : '已停用',
    status_class: item.status === 'normal' ? 'member-status normal' : 'member-status disabled',
    role_action_text: item.role === 'team_leader' ? '改为工作人员' : '设为负责人',
    status_action_text: item.status === 'normal' ? '停用' : '启用'
  }))
}

Page({
  data: {
    adminAgencies: [],
    agencyId: 0,
    agencyName: '',
    hasAdminAgency: false,
    groups: [],
    activeGroups: [],
    assignmentGroupIndex: 0,
    assignmentGroupLabel: '',
    filterOptions: [{ id: 0, name: '全部团队' }],
    filterGroupIndex: 0,
    selectedFilterLabel: '全部团队',
    members: [],
    visibleMembers: [],
    hasVisibleMembers: false,
    roleOptions,
    roleIndex: 0,
    selectedRoleLabel: roleOptions[0].label,
    form: emptyForm(),
    editingMemberId: 0,
    editingMemberStatus: 'normal',
    editingMemberUpdatedAt: '',
    submitTitle: '添加成员',
    loading: false,
    saving: false,
    error: ''
  },

  onShow() {
    this.loadAdmin()
  },

  loadAdmin() {
    this.setData({ loading: true, error: '' })
    auth.me()
      .then(user => {
        const agencies = Array.isArray(user.admin_agencies) ? user.admin_agencies : []
        const agency = agencies[0]
        this.setData({
          adminAgencies: agencies,
          agencyId: agency ? agency.agency_id : 0,
          agencyName: agency ? agency.agency_name : '',
          hasAdminAgency: Boolean(agency)
        })
        return agency ? this.loadData(agency.agency_id) : null
      })
      .catch(err => this.showError(err, '获取管理员权限失败'))
      .finally(() => this.setData({ loading: false }))
  },

  loadData(agencyId) {
    this.setData({ loading: true, error: '' })
    return Promise.all([adminGoods.listGroups(agencyId), adminStaff.list(agencyId)])
      .then(([groups, members]) => {
        const activeGroups = groups.filter(group => group.status === 'normal')
        const selectedGroup = activeGroups[this.data.assignmentGroupIndex] || activeGroups[0]
        this.setData({
          groups,
          activeGroups,
          assignmentGroupIndex: selectedGroup ? Math.max(0, activeGroups.findIndex(group => group.id === selectedGroup.id)) : 0,
          assignmentGroupLabel: selectedGroup ? selectedGroup.name : '',
          filterOptions: [{ id: 0, name: '全部团队' }].concat(groups),
          members: presentMembers(members)
        })
        this.applyFilter()
      })
      .catch(err => this.showError(err, '人员数据加载失败'))
      .finally(() => this.setData({ loading: false }))
  },

  applyFilter() {
    const option = this.data.filterOptions[this.data.filterGroupIndex]
    const groupId = option ? option.id : 0
    const visibleMembers = groupId
      ? this.data.members.filter(member => member.group_id === groupId)
      : this.data.members
    this.setData({
      visibleMembers,
      hasVisibleMembers: visibleMembers.length > 0,
      selectedFilterLabel: option ? option.name : '全部团队'
    })
  },

  handleFilterChange(event) {
    this.setData({ filterGroupIndex: Number(event.detail.value || 0) })
    this.applyFilter()
  },

  handleGroupChange(event) {
    const index = Number(event.detail.value || 0)
    const group = this.data.activeGroups[index]
    this.setData({
      assignmentGroupIndex: index,
      assignmentGroupLabel: group ? group.name : ''
    })
  },

  handleRoleChange(event) {
    const roleIndex = Number(event.detail.value || 0)
    const role = roleOptions[roleIndex] || roleOptions[0]
    this.setData({ roleIndex, selectedRoleLabel: role.label })
  },

  handleInput(event) {
    const field = event.currentTarget.dataset.field
    if (!field) {
      return
    }
    this.setData({ form: Object.assign({}, this.data.form, { [field]: event.detail.value || '' }) })
  },

  submit() {
    if (this.data.saving) {
      return
    }
    const userId = Number(this.data.form.user_id)
    const group = this.data.activeGroups[this.data.assignmentGroupIndex]
    const role = roleOptions[this.data.roleIndex] || roleOptions[0]
    if (!Number.isSafeInteger(userId) || userId <= 0) {
      wx.showToast({ title: '请输入正确会员编号', icon: 'none' })
      return
    }
    if (!group) {
      wx.showToast({ title: '当前没有可用团队', icon: 'none' })
      return
    }
    const payload = {
      agency_id: this.data.agencyId,
      group_id: group.id,
      user_id: userId,
      role: role.value,
      display_name: this.data.form.display_name.trim(),
      remark: this.data.form.remark.trim()
    }
    this.setData({ saving: true, error: '' })
    const action = this.data.editingMemberId
      ? adminStaff.update(this.data.editingMemberId, {
        role: payload.role,
        status: this.data.editingMemberStatus,
        display_name: payload.display_name,
        remark: payload.remark,
        expected_updated_at: this.data.editingMemberUpdatedAt
      })
      : adminStaff.save(payload)
    action
      .then(() => {
        wx.showToast({ title: this.data.editingMemberId ? '已保存' : '已添加', icon: 'success' })
        this.resetForm()
        return this.loadData(this.data.agencyId)
      })
      .catch(err => this.showError(err, '保存成员失败'))
      .finally(() => this.setData({ saving: false }))
  },

  editMember(event) {
    const member = this.findMember(event)
    if (!member) {
      return
    }
    const groupIndex = this.data.activeGroups.findIndex(group => group.id === member.group_id)
    const roleIndex = roleOptions.findIndex(role => role.value === member.role)
    this.setData({
      editingMemberId: member.id,
      editingMemberStatus: member.status,
      editingMemberUpdatedAt: member.updated_at,
      assignmentGroupIndex: groupIndex >= 0 ? groupIndex : 0,
      assignmentGroupLabel: member.group_name,
      roleIndex: roleIndex >= 0 ? roleIndex : 0,
      selectedRoleLabel: roleOptions[roleIndex >= 0 ? roleIndex : 0].label,
      form: {
        user_id: String(member.user_id),
        display_name: member.display_name || '',
        remark: member.remark || ''
      },
      submitTitle: '保存成员'
    })
    wx.pageScrollTo({ scrollTop: 0, duration: 200 })
  },

  toggleRole(event) {
    const member = this.findMember(event)
    if (!member || this.data.saving) {
      return
    }
    const nextRole = member.role === 'team_leader' ? 'staff' : 'team_leader'
    const nextLabel = nextRole === 'team_leader' ? '团队负责人' : '工作人员'
    wx.showModal({
      title: '变更成员角色',
      content: `确认将「${member.name_text}」设为${nextLabel}吗？`,
      confirmText: '确认变更',
      confirmColor: '#207f79',
      success: res => {
        if (res.confirm) {
          this.updateMember(member, { role: nextRole })
        }
      }
    })
  },

  toggleStatus(event) {
    const member = this.findMember(event)
    if (!member || this.data.saving) {
      return
    }
    const disabling = member.status === 'normal'
    wx.showModal({
      title: disabling ? '停用成员' : '启用成员',
      content: disabling
        ? `停用后「${member.name_text}」将立即失去该团队的工作人员权限。`
        : `确认恢复「${member.name_text}」在该团队的权限吗？`,
      confirmText: disabling ? '确认停用' : '确认启用',
      confirmColor: disabling ? '#c04736' : '#207f79',
      success: res => {
        if (!res.confirm) {
          return
        }
        if (disabling) {
          this.disableMember(member)
        } else {
          this.updateMember(member, { status: 'normal' })
        }
      }
    })
  },

  updateMember(member, changes) {
    const payload = {
      role: changes.role || member.role,
      status: changes.status || member.status,
      display_name: member.display_name || '',
      remark: member.remark || '',
      expected_updated_at: member.updated_at
    }
    this.setData({ saving: true, error: '' })
    adminStaff.update(member.id, payload)
      .then(() => {
        wx.showToast({ title: '已更新', icon: 'success' })
        return this.loadData(this.data.agencyId)
      })
      .catch(err => this.showError(err, '更新成员失败'))
      .finally(() => this.setData({ saving: false }))
  },

  disableMember(member) {
    this.setData({ saving: true, error: '' })
    adminStaff.disable(member.id)
      .then(() => {
        wx.showToast({ title: '已停用', icon: 'success' })
        return this.loadData(this.data.agencyId)
      })
      .catch(err => this.showError(err, '停用成员失败'))
      .finally(() => this.setData({ saving: false }))
  },

  findMember(event) {
    const id = Number(event.currentTarget.dataset.id)
    return this.data.members.find(member => member.id === id)
  },

  resetForm() {
    const group = this.data.activeGroups[0]
    this.setData({
      form: emptyForm(),
      editingMemberId: 0,
      editingMemberStatus: 'normal',
      editingMemberUpdatedAt: '',
      submitTitle: '添加成员',
      assignmentGroupIndex: 0,
      assignmentGroupLabel: group ? group.name : '',
      roleIndex: 0,
      selectedRoleLabel: roleOptions[0].label
    })
  },

  showError(err, fallback) {
    const errorMessages = {
      last_team_leader: '请先设置另一名在职团队负责人',
      user_not_found: '未找到该会员，或会员账号已停用',
      group_not_found: '所选团队不可用',
      admin_agency_forbidden: '当前账号没有人员管理权限',
      invalid_staff_role: '成员角色不正确',
      staff_member_changed: '成员资料已被其他管理员修改，请刷新后重试',
      missing_staff_version: '成员版本信息缺失，请刷新后重试'
    }
    const message = errorMessages[err.code] || err.message || fallback
    this.setData({ error: message })
    wx.showToast({ title: message, icon: 'none' })
    if ((err.code === 'staff_member_changed' || err.code === 'missing_staff_version') && this.data.agencyId) {
      this.resetForm()
      this.loadData(this.data.agencyId)
    }
  }
})
