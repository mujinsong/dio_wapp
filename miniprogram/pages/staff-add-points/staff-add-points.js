const auth = require('../../utils/auth')
const points = require('../../utils/points')
const operationRequest = require('../../utils/operationRequest')

const MAX_POINTS_PER_GRANT = 100000
const LARGE_GRANT_CONFIRM_THRESHOLD = 10000
const paymentMethods = [
  { label: '微信支付', value: 'wechat' },
  { label: '现金', value: 'cash' },
  { label: '支付宝', value: 'alipay' },
  { label: '银行卡', value: 'card' },
  { label: '其他', value: 'other' }
]

function normalizeToken(raw) {
  const text = (raw || '').trim()
  if (!text) {
    return ''
  }
  const prefix = 'dio_identity:'
  if (text.indexOf(prefix) === 0) {
    return text.slice(prefix.length)
  }
  return text
}

function calculateQuickPoints(ticketPrice, ticketCount) {
  const price = Number(ticketPrice)
  const count = Number(ticketCount)
  if (!Number.isInteger(price) || price <= 0 || !Number.isInteger(count) || count <= 0) {
    return 0
  }
  const total = price * count
  return Number.isSafeInteger(total) ? total : 0
}

Page({
  data: {
    staffGroups: [],
    groupIndex: 0,
    selectedGroupLabel: '',
    hasStaffGroups: false,
    identityToken: '',
    rawScanText: '',
    pointMode: 'quick',
    ticketPrice: '',
    ticketCount: '',
    quickPoints: 0,
    paymentMethods,
    paymentMethodIndex: 0,
    selectedPaymentMethodLabel: paymentMethods[0].label,
    pointValue: '',
    remark: '',
    loading: false,
    pendingGrantRequest: null,
    result: null,
    error: ''
  },

  onLoad() {
    this.loadMe()
  },

  loadMe() {
    auth.me()
      .then(user => {
        const staffGroups = Array.isArray(user.staff_groups) ? user.staff_groups : []
        this.setGroupState(staffGroups, 0)
      })
      .catch(err => {
        wx.showToast({ title: err.message || '获取员工信息失败', icon: 'none' })
      })
  },

  handleGroupChange(event) {
    this.setGroupState(this.data.staffGroups, Number(event.detail.value || 0))
  },

  setGroupState(staffGroups, groupIndex) {
    const group = staffGroups[groupIndex]
    this.setData({
      staffGroups,
      groupIndex,
      hasStaffGroups: staffGroups.length > 0,
      selectedGroupLabel: group ? `${group.agency_name} · ${group.group_name}` : ''
    })
  },

  handleTokenInput(event) {
    const rawScanText = event.detail.value || ''
    this.setData({
      rawScanText,
      identityToken: normalizeToken(rawScanText)
    })
  },

  handlePointInput(event) {
    this.setData({ pointValue: event.detail.value || '' })
  },

  handleModeChange(event) {
    const pointMode = event.currentTarget.dataset.mode
    if (pointMode !== 'quick' && pointMode !== 'manual') {
      return
    }
    this.setData({ pointMode, result: null, error: '' })
  },

  handleTicketPriceInput(event) {
    const ticketPrice = event.detail.value || ''
    this.setData({
      ticketPrice,
      quickPoints: calculateQuickPoints(ticketPrice, this.data.ticketCount)
    })
  },

  handleTicketCountInput(event) {
    const ticketCount = event.detail.value || ''
    this.setData({
      ticketCount,
      quickPoints: calculateQuickPoints(this.data.ticketPrice, ticketCount)
    })
  },

  handlePaymentMethodChange(event) {
    const paymentMethodIndex = Number(event.detail.value || 0)
    const method = this.data.paymentMethods[paymentMethodIndex] || this.data.paymentMethods[0]
    this.setData({
      paymentMethodIndex,
      selectedPaymentMethodLabel: method.label
    })
  },

  handleRemarkInput(event) {
    this.setData({ remark: event.detail.value || '' })
  },

  scanCode() {
    wx.scanCode({
      onlyFromCamera: true,
      scanType: ['qrCode'],
      success: res => {
        const rawScanText = res.result || ''
        this.setData({
          rawScanText,
          identityToken: normalizeToken(rawScanText),
          result: null,
          error: ''
        })
      },
      fail: err => {
        const message = err && err.errMsg && err.errMsg.indexOf('cancel') >= 0 ? '已取消扫码' : '扫码失败'
        wx.showToast({ title: message, icon: 'none' })
      }
    })
  },

  submit() {
    if (this.data.loading) {
      return
    }
    const group = this.data.staffGroups[this.data.groupIndex]
    const isQuickMode = this.data.pointMode === 'quick'
    const ticketPrice = Number(this.data.ticketPrice)
    const ticketCount = Number(this.data.ticketCount)
    const pointValue = isQuickMode
      ? calculateQuickPoints(this.data.ticketPrice, this.data.ticketCount)
      : Number(this.data.pointValue)

    if (!group) {
      wx.showToast({ title: '当前账号没有员工团体权限', icon: 'none' })
      return
    }
    if (!this.data.identityToken) {
      wx.showToast({ title: '请先扫描用户二维码', icon: 'none' })
      return
    }
    if (isQuickMode && (!Number.isInteger(ticketPrice) || ticketPrice <= 0)) {
      wx.showToast({ title: '请输入正确的单张面额', icon: 'none' })
      return
    }
    if (isQuickMode && (!Number.isInteger(ticketCount) || ticketCount <= 0)) {
      wx.showToast({ title: '请输入正确的购买张数', icon: 'none' })
      return
    }
    if (!Number.isInteger(pointValue) || pointValue <= 0) {
      wx.showToast({ title: '请输入正确积分', icon: 'none' })
      return
    }
    if (pointValue > MAX_POINTS_PER_GRANT) {
      wx.showToast({ title: `单次最多增加 ${MAX_POINTS_PER_GRANT} 积分`, icon: 'none' })
      return
    }

    const purchaseRemark = `线下购券：${ticketPrice}元 × ${ticketCount}张`
    const remark = isQuickMode
      ? (this.data.remark ? `${purchaseRemark}；${this.data.remark}` : purchaseRemark)
      : this.data.remark

    if (pointValue >= LARGE_GRANT_CONFIRM_THRESHOLD) {
	  const requiresApproval = group.member_role !== 'team_leader'
      wx.showModal({
		title: requiresApproval ? '提交大额审批' : '确认大额积分',
		content: requiresApproval
		  ? `本次 ${pointValue} 积分将提交团队负责人审批，通过后才会入账。`
		  : `本次将增加 ${pointValue} 积分，请核对金额和张数。`,
		confirmText: requiresApproval ? '提交审批' : '确认发放',
        success: res => {
          if (res.confirm) {
            this.grantPoints(group, pointValue, remark)
          }
        }
      })
      return
    }

    this.grantPoints(group, pointValue, remark)
  },

  grantPoints(group, pointValue, remark) {
    if (this.data.loading) {
      return
    }
    const payload = {
      agency_id: group.agency_id,
      group_id: group.group_id,
      identity_token: this.data.identityToken,
      points: pointValue,
	  grant_mode: this.data.pointMode === 'quick' ? 'ticket' : 'manual',
	  ticket_unit_price: this.data.pointMode === 'quick' ? Number(this.data.ticketPrice) : 0,
	  ticket_count: this.data.pointMode === 'quick' ? Number(this.data.ticketCount) : 0,
	  payment_method: this.data.pointMode === 'quick'
	    ? this.data.paymentMethods[this.data.paymentMethodIndex].value
	    : '',
      remark
    }
    const operation = operationRequest.resolve('points', this.data.pendingGrantRequest, payload)
    this.setData({ loading: true, error: '', result: null, pendingGrantRequest: operation })
    points.staffAddPoints(Object.assign({}, payload, { request_id: operation.id }))
      .then(result => {
        this.setData({
          result,
          pointValue: '',
          ticketCount: '',
          quickPoints: 0,
          remark: '',
          identityToken: '',
          rawScanText: '',
          pendingGrantRequest: null
        })
		wx.showToast({
		  title: result.pending_approval ? '已提交负责人审批' : '加积分成功',
		  icon: result.pending_approval ? 'none' : 'success'
		})
      })
      .catch(err => {
		const messages = {
		  staff_daily_points_limit: '今日积分发放额度不足，请联系管理员',
		  invalid_ticket_sale: '购券面额或张数不正确',
		  invalid_payment_method: '请选择正确的收款方式',
		  points_too_large: `单次最多增加 ${MAX_POINTS_PER_GRANT} 积分`,
		  qr_token_expired: '用户二维码已过期，请重新扫码',
		  qr_token_not_found: '用户二维码无效或已经使用',
		  points_balance_limit: '用户积分余额已达上限'
		}
		const message = messages[err && err.code] || err.message || '加积分失败'
        this.setData({ error: message })
        wx.showToast({ title: message, icon: 'none' })
      })
      .finally(() => {
        this.setData({ loading: false })
      })
  }
})
