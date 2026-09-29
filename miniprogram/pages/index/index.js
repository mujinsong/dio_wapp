const auth = require('../../utils/auth')

function presentUser(user) {
  if (!user) {
    return null
  }

  return Object.assign({}, user, {
    avatar_text: (user.nickname || 'D').slice(0, 1).toUpperCase(),
    nickname_display: user.nickname || '微信用户',
    member_no: user.id || '-',
    points_balance_display: user.points_balance || 0,
    is_staff: Array.isArray(user.roles) && user.roles.indexOf('staff') >= 0,
    is_team_leader: Array.isArray(user.roles) && user.roles.indexOf('team_leader') >= 0,
    is_admin: Array.isArray(user.roles) && user.roles.indexOf('admin') >= 0,
    status_text: user.status === 'normal' ? '正常' : (user.status || '-')
  })
}

Page({
  data: {
    user: null,
    loading: false,
    error: ''
  },

  onLoad() {
    const token = wx.getStorageSync('token')
    const cachedUser = wx.getStorageSync('user')
    if (token && cachedUser) {
      this.setData({ user: presentUser(cachedUser) })
    }
    if (token) {
      this.handleRefresh()
    }
  },

  onShow() {
    if (wx.getStorageSync('token') && !this.data.loading) {
      this.handleRefresh()
    }
  },

  handleLogin() {
    if (this.data.loading) return
    const version = this._authVersion = (this._authVersion || 0) + 1
    this.setData({ loading: true, error: '' })
    auth.login()
      .then(user => {
        if (version !== this._authVersion) return
        this.setData({ user: presentUser(user) })
      })
      .catch(err => {
        if (version !== this._authVersion || err.code === 'session_changed') return
        const message = err.message || '登录失败'
        this.setData({ error: message })
        wx.showToast({ title: message, icon: 'none' })
      })
      .finally(() => {
        if (version !== this._authVersion) return
        this.setData({ loading: false })
      })
  },

  handleRefresh() {
    const version = this._authVersion = (this._authVersion || 0) + 1
    this.setData({ loading: true, error: '' })
    auth.me()
      .then(user => {
        if (version !== this._authVersion) return
        this.setData({ user: presentUser(user) })
      })
      .catch(err => {
        if (version !== this._authVersion || err.code === 'session_changed') return
        const expiredCodes = ['missing_token', 'invalid_token', 'user_disabled']
        if (expiredCodes.indexOf(err.code) >= 0) {
          auth.logout()
          this.setData({ user: null, error: err.message || '登录已失效' })
          return
        }
        const message = err.message || '账户信息刷新失败'
        this.setData({ error: message })
        wx.showToast({ title: message, icon: 'none' })
      })
      .finally(() => {
        if (version !== this._authVersion) return
        this.setData({ loading: false })
      })
  },

  handleLogout() {
    this._authVersion = (this._authVersion || 0) + 1
    auth.logout()
    this.setData({ user: null, error: '', loading: false })
  },

  onUnload() {
    this._authVersion = (this._authVersion || 0) + 1
  },

  goProfile() {
    wx.navigateTo({ url: '/pages/profile/profile' })
  },

  goIdentityQR() {
    wx.navigateTo({ url: '/pages/identity-qr/identity-qr' })
  },

  goShop() {
    wx.navigateTo({ url: '/pages/shop/shop' })
  },

  goExchangeOrders() {
    wx.navigateTo({ url: '/pages/exchange-orders/exchange-orders' })
  },

  goPointLedgers() {
    wx.navigateTo({ url: '/pages/point-ledgers/point-ledgers' })
  },

  goAdminGoods() {
    wx.navigateTo({ url: '/pages/admin-goods/admin-goods' })
  },

  goAdminStaff() {
    wx.navigateTo({ url: '/pages/admin-staff/admin-staff' })
  },

  goAdminPoints() {
    wx.navigateTo({ url: '/pages/admin-points/admin-points' })
  },

  goAdminReport() {
    wx.navigateTo({ url: '/pages/admin-report/admin-report' })
  },

  goAdminOperations() {
    wx.navigateTo({ url: '/pages/admin-operations/admin-operations' })
  },

  goStaffAddPoints() {
    wx.navigateTo({ url: '/pages/staff-add-points/staff-add-points' })
  },

  goStaffWorkbench() {
    wx.navigateTo({ url: '/pages/staff-workbench/staff-workbench' })
  },

  goStaffRedeemOrder() {
    wx.navigateTo({ url: '/pages/staff-redeem-order/staff-redeem-order' })
  },

  goStaffGoods() {
    wx.navigateTo({ url: '/pages/staff-goods/staff-goods' })
  },

  goPointGrantApprovals() {
	wx.navigateTo({ url: '/pages/point-grant-approvals/point-grant-approvals' })
  },

  goPointCorrections() {
    wx.navigateTo({ url: '/pages/point-corrections/point-corrections' })
  }
})
