const auth = require('../../utils/auth')
const points = require('../../utils/points')
const operationRequest = require('../../utils/operationRequest')
const session = require('../../utils/session')

const typeNames = {
  staff_add: '工作人员授予',
  exchange_cost: '商品兑换',
  order_refund: '取消退款',
  admin_adjust: '管理员纠错',
  grant_reversal: '积分冲正'
}

function formatTime(raw) {
  const date = new Date(raw)
  if (!Number.isFinite(date.getTime())) {
    return '-'
  }
  const pad = value => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function presentLedger(item) {
  const delta = Number(item.delta_points || 0)
  return Object.assign({}, item, {
    type_text: typeNames[item.type] || '积分变动',
    delta_text: `${delta > 0 ? '+' : ''}${delta}`,
    delta_class: delta >= 0 ? 'history-delta positive' : 'history-delta negative',
    time_text: formatTime(item.created_at),
    remark_text: item.remark || '无备注',
    operator_text: item.operator_id ? `操作人 #${item.operator_id}` : '系统记录'
  })
}

Page({
  data: {
    agencies: [],
    agencyIndex: 0,
    agencyId: 0,
    agencyName: '',
    userIdInput: '',
    target: null,
    hasTarget: false,
    mode: 'add',
    amount: '',
    remark: '',
    ledgers: [],
    searching: false,
    saving: false,
    confirming: false,
    pendingAdjustmentRequest: null,
    loading: true,
    error: ''
  },

  onLoad() {
    this.loadAdmin()
  },

  onPullDownRefresh() {
    if (this.data.saving || this.data.confirming) { wx.stopPullDownRefresh(); return }
    const action = this.data.hasTarget ? this.loadTarget(this.data.target.id) : this.loadAdmin()
    action.finally(() => wx.stopPullDownRefresh())
  },

  loadAdmin() {
    this.setData({ loading: true, error: '' })
    return auth.me()
      .then(user => {
        const agencies = Array.isArray(user.admin_agencies) ? user.admin_agencies : []
        const agency = agencies[this.data.agencyIndex] || agencies[0]
        this.setData({
          agencies,
          agencyId: agency ? agency.agency_id : 0,
          agencyName: agency ? agency.agency_name : '',
          agencyIndex: agency ? Math.max(0, agencies.findIndex(item => item.agency_id === agency.agency_id)) : 0
        })
      })
      .catch(err => this.showError(err, '管理员权限加载失败'))
      .finally(() => this.setData({ loading: false }))
  },

  handleAgencyChange(event) {
    if (this.data.saving || this.data.confirming) return
    this._targetVersion = (this._targetVersion || 0) + 1
    const agencyIndex = Number(event.detail.value || 0)
    const agency = this.data.agencies[agencyIndex]
    this.setData({
      agencyIndex,
      agencyId: agency ? agency.agency_id : 0,
      agencyName: agency ? agency.agency_name : '',
      target: null,
      hasTarget: false,
      ledgers: [],
      searching: false,
      error: ''
    })
  },

  handleUserInput(event) {
    this.setData({ userIdInput: event.detail.value || '' })
  },

  handleAmountInput(event) {
    this.setData({ amount: event.detail.value || '' })
  },

  handleRemarkInput(event) {
    this.setData({ remark: event.detail.value || '' })
  },

  setMode(event) {
    if (!this.data.saving) {
      this.setData({ mode: event.currentTarget.dataset.mode === 'deduct' ? 'deduct' : 'add' })
    }
  },

  searchUser() {
    if (this.data.saving || this.data.confirming) return
    const userId = Number(this.data.userIdInput)
    if (!Number.isSafeInteger(userId) || userId <= 0) {
      wx.showToast({ title: '请输入正确会员编号', icon: 'none' })
      return
    }
    this.loadTarget(userId)
  },

  loadTarget(userId) {
    const version = this._targetVersion = (this._targetVersion || 0) + 1
    if (!this.data.agencyId) {
      return Promise.resolve()
    }
    this.setData({ searching: true, error: '' })
    const agencyId = this.data.agencyId
    return Promise.all([
      points.getAdminPointUser(agencyId, userId),
      points.listAdminUserLedgers(agencyId, userId, 0, 10)
    ])
      .then(([target, page]) => {
        if (version !== this._targetVersion) return
        this.setData({
          target,
          hasTarget: true,
          userIdInput: String(target.id),
          ledgers: (page.items || []).map(presentLedger)
        })
      })
      .catch(err => {
        if (version !== this._targetVersion || err.code === 'session_changed') return
        this.setData({ target: null, hasTarget: false, ledgers: [] })
        this.showError(err, '会员查询失败')
      })
      .finally(() => {
        if (version === this._targetVersion) this.setData({ searching: false, loading: false })
      })
  },

  submitAdjustment() {
    if (!this.data.hasTarget || this.data.saving || this.data.confirming || this.data.searching || this.data.loading) {
      return
    }
    const amount = Number(this.data.amount)
    const remark = this.data.remark.trim()
    if (!Number.isSafeInteger(amount) || amount <= 0 || amount > 100000) {
      wx.showToast({ title: '金额需为 1 至 100000 的整数', icon: 'none' })
      return
    }
    if (remark.length < 2 || remark.length > 512) {
      wx.showToast({ title: '请填写 2 至 512 字纠错原因', icon: 'none' })
      return
    }
    const delta = this.data.mode === 'deduct' ? -amount : amount
    const actionText = delta > 0 ? '补加' : '扣减'
    const payload = Object.freeze({ agency_id: this.data.agencyId, user_id: this.data.target.id, delta_points: delta, remark })
    const snapshot = session.capture()
    this.setData({ confirming: true })
    wx.showModal({
      title: '确认积分纠错',
      content: `将为会员 #${payload.user_id} ${actionText} ${amount} 积分，操作会记录审计流水。`,
      confirmText: '确认提交',
      success: result => {
        if (this._unloaded) return
        this.setData({ confirming: false })
        if (result.confirm && session.isCurrent(snapshot) && !this._unloaded &&
          this.data.agencyId === payload.agency_id && this.data.target && this.data.target.id === payload.user_id) {
          this.performAdjustment(payload, snapshot)
        }
      },
      fail: () => { if (!this._unloaded) this.setData({ confirming: false }) }
    })
  },

  performAdjustment(payload, snapshot) {
    if (this.data.saving || this._unloaded || !session.isCurrent(snapshot)) return
    const operation = operationRequest.resolve('admin_adjust', this.data.pendingAdjustmentRequest, payload, true)
    this.setData({ saving: true, error: '', pendingAdjustmentRequest: operation })
    return points.adminAdjustPoints(Object.assign({}, payload, { request_id: operation.id }))
      .then(result => {
        session.assertCurrent(snapshot)
        operationRequest.clear('admin_adjust', operation.id, true)
        if (this._unloaded) return
        wx.showToast({ title: '积分已纠正', icon: 'success' })
        this.setData({ amount: '', remark: '', pendingAdjustmentRequest: null })
        return this.loadTarget(result.user_id)
      })
      .catch(err => { if (!this._unloaded && err.code !== 'session_changed') this.showError(err, '积分纠错失败') })
      .finally(() => { if (!this._unloaded) this.setData({ saving: false }) })
  },

  onUnload() {
    this._unloaded = true
    this._targetVersion = (this._targetVersion || 0) + 1
  },

  showError(err, fallback) {
    const messages = {
      admin_forbidden: '没有该事务所的管理员权限',
      user_not_found: '未找到该会员',
      points_balance_limit: '调整后积分会超出允许范围',
      invalid_points: '积分调整金额不正确',
      invalid_remark: '纠错原因不符合要求'
    }
    const message = messages[err && err.code] || (err && err.message) || fallback
    this.setData({ error: message })
    wx.showToast({ title: message, icon: 'none' })
  }
})
