const auth = require('../../utils/auth')
const adminReport = require('../../utils/adminReport')
const session = require('../../utils/session')

const movementNames = {
  initial: '初始库存',
  admin_adjust: '管理员调整',
  goods_approval: '商品申请生效',
  exchange: '兑换扣减',
  order_cancel: '取消返库',
  order_expire: '超时返库'
}

function dateText(date) {
  const pad = value => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

function defaultRange() {
  const end = new Date()
  const start = new Date(end.getFullYear(), end.getMonth(), end.getDate() - 6)
  return { dateFrom: dateText(start), dateTo: dateText(end) }
}

function formatTime(raw) {
  const date = new Date(raw)
  if (!Number.isFinite(date.getTime())) {
    return '-'
  }
  const pad = value => String(value).padStart(2, '0')
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function presentMovement(item) {
  const delta = Number(item.delta_stock || 0)
  return Object.assign({}, item, {
    type_text: movementNames[item.type] || '库存变动',
    delta_text: `${delta > 0 ? '+' : ''}${delta}`,
    delta_class: delta >= 0 ? 'movement-delta increase' : 'movement-delta decrease',
    time_text: formatTime(item.created_at),
    operator_text: item.operator_name || (item.operator_id ? `操作人 #${item.operator_id}` : '系统')
  })
}

Page({
  data: Object.assign({
    agencies: [],
    agencyIndex: 0,
    agencyId: 0,
    agencyName: '',
    report: null,
    metrics: {},
    staffStats: [],
    movements: [],
    loading: true,
    reconciling: false,
    reconciliation: null,
    reconciliationError: '',
    error: ''
  }, defaultRange()),

  onLoad() {
    return this.loadAdmin()
  },

  onShow() {
    if (!this._shown) {
      this._shown = true
      return
    }
    return this.loadAdmin()
  },

  clearSensitiveData() {
    this._adminVersion = (this._adminVersion || 0) + 1
    this._reportVersion = (this._reportVersion || 0) + 1
    this._reconciliationVersion = (this._reconciliationVersion || 0) + 1
    this._reconciliationSession = null
    this.setData({ agencies: [], agencyIndex: 0, agencyId: 0, agencyName: '',
      report: null, metrics: {}, staffStats: [], movements: [],
      reconciliation: null, reconciliationError: '', reconciling: false, loading: false })
  },

  onUnload() {
    this._unloaded = true
    this._adminVersion = (this._adminVersion || 0) + 1
    this._reportVersion = (this._reportVersion || 0) + 1
    this._reconciliationVersion = (this._reconciliationVersion || 0) + 1
  },

  async reconcile() {
    if (this.data.reconciling || !this.data.agencyId) return
    const version = this._reconciliationVersion = (this._reconciliationVersion || 0) + 1
    const snapshot = session.capture()
    const agencyId = this.data.agencyId
    const current = () => !this._unloaded && this._reconciliationVersion === version && this.data.agencyId === agencyId && session.isCurrent(snapshot)
    let state = this.data.reconciliation
    if (!state || state.done || !this._reconciliationSession || !session.isCurrent(this._reconciliationSession)) {
      state = { kindIndex: 0, cursor: 0, checked: 0, issueCount: 0, issues: [], done: false }
    }
    this._reconciliationSession = snapshot
    this.setData({ reconciling: true, reconciliation: state, reconciliationError: '' })
    const kinds = ['accounts', 'orders', 'stock']
    const names = { accounts: '积分账户', orders: '兑换订单', stock: '商品库存' }
    try {
      for (let batch = 0; batch < 3 && !state.done; batch++) {
        const page = await adminReport.reconcile(agencyId, kinds[state.kindIndex], state.cursor)
        if (!current()) return
        const issues = (page.issues || []).map(item => Object.assign({}, item, { label: names[item.kind], severityText: item.severity === 'warning' ? '待核对' : '异常' }))
        const nextKind = page.has_more ? state.kindIndex : state.kindIndex + 1
        state = Object.assign({}, state, {
          kindIndex: nextKind, cursor: page.has_more ? page.next_cursor : 0,
          checked: state.checked + page.checked,
          issueCount: state.issueCount + issues.length,
          issues: state.issues.concat(issues).slice(0, 200),
          done: nextKind >= kinds.length,
          checkedAt: formatTime(page.checked_at)
        })
        this.setData({ reconciliation: state })
      }
    } catch (err) {
      if (current()) {
        if (['admin_forbidden', 'invalid_token', 'missing_token', 'user_disabled'].includes(err.code)) this.showError(err, '核对权限已失效')
        else this.setData({ reconciliationError: err.message || '核对失败，已保留当前进度' })
      }
    } finally {
      if (!this._unloaded && this._reconciliationVersion === version) this.setData({ reconciling: false })
    }
  },

  onPullDownRefresh() {
    return this.loadAdmin().finally(() => wx.stopPullDownRefresh())
  },

  loadAdmin() {
    if (this._unloaded) return Promise.resolve()
    this.clearSensitiveData()
    const version = this._adminVersion
    const snapshot = session.capture()
    const current = () => !this._unloaded && version === this._adminVersion && session.isCurrent(snapshot)
    this.setData({ loading: true, error: '' })
    return auth.me()
      .then(user => {
        if (!current()) return
        const agencies = Array.isArray(user.admin_agencies) ? user.admin_agencies : []
        const agency = agencies[0]
        this.setData({
          agencies,
          agencyIndex: 0,
          agencyId: agency ? agency.agency_id : 0,
          agencyName: agency ? agency.agency_name : ''
        })
        if (!agency) {
          throw new Error('当前账号没有管理员权限')
        }
        return this.loadReport()
      })
      .catch(err => {
        if (!current()) return
        this.setData({ loading: false })
        this.showError(err, '经营数据加载失败')
      })
  },

  loadReport() {
    if (this._unloaded) return Promise.resolve()
    const version = this._reportVersion = (this._reportVersion || 0) + 1
    const snapshot = session.capture()
    const current = () => !this._unloaded && version === this._reportVersion && session.isCurrent(snapshot)
    if (!this.data.agencyId) {
      return Promise.resolve()
    }
    this.setData({ loading: true, error: '', report: null, metrics: {}, staffStats: [], movements: [] })
    return adminReport.overview(this.data.agencyId, this.data.dateFrom, this.data.dateTo)
      .then(report => {
        if (!current()) return
        this.setData({
          report,
          metrics: report.metrics || {},
          staffStats: report.staff_stats || [],
          movements: (report.stock_movements || []).map(presentMovement)
        })
      })
      .catch(err => { if (current()) this.showError(err, '经营数据加载失败') })
      .finally(() => { if (current()) this.setData({ loading: false }) })
  },

  handleAgencyChange(event) {
    this._reconciliationVersion = (this._reconciliationVersion || 0) + 1
    const agencyIndex = Number(event.detail.value || 0)
    const agency = this.data.agencies[agencyIndex]
    this.setData({
      report: null,
      metrics: {},
      staffStats: [],
      movements: [],
      reconciliation: null,
      reconciliationError: '',
      reconciling: false,
      agencyIndex,
      agencyId: agency ? agency.agency_id : 0,
      agencyName: agency ? agency.agency_name : ''
    })
    this.loadReport()
  },

  handleDateFrom(event) {
    this.setData({ dateFrom: event.detail.value })
  },

  handleDateTo(event) {
    this.setData({ dateTo: event.detail.value })
  },

  applyRange() {
    if (this.data.dateFrom > this.data.dateTo) {
      wx.showToast({ title: '开始日期不能晚于结束日期', icon: 'none' })
      return
    }
    this.loadReport()
  },

  showError(err, fallback) {
    if (err && ['admin_forbidden', 'invalid_token', 'missing_token', 'user_disabled'].includes(err.code)) this.clearSensitiveData()
    const messages = {
      admin_forbidden: '没有该事务所的管理员权限',
      invalid_date_range: '日期范围应为 1 至 90 天'
    }
    const message = messages[err && err.code] || (err && err.message) || fallback
    this.setData({ error: message })
    wx.showToast({ title: message, icon: 'none' })
  }
})
