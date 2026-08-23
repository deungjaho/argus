// Runtime8 Fixture: after-sales form page logic (Scenario A & D)
const app = getApp()

Page({
  data: {
    reasonValue: '',
    contactValue: '',
    canSubmit: false,
    submitLabel: '提交申请',
    submitStatus: '待填写',
    keyboardHideCount: 0,
    savedRunId: ''
  },

  // Root tap handler — mirrors the real Lacoeur hideKeyboard issue.
  // Any bubbled tap event increments this counter.
  hideKeyboard() {
    this.setData({ keyboardHideCount: this.data.keyboardHideCount + 1 })
  },

  onReasonInput(e) {
    const val = e.detail.value
    this.setData({
      reasonValue: val,
      canSubmit: val.trim().length > 0
    })
  },

  onContactInput(e) {
    this.setData({ contactValue: e.detail.value })
  },

  onSubmit() {
    if (!this.data.canSubmit) return
    this.setData({ submitLabel: '提交中', submitStatus: '提交中' })

    // Simulate async submission (300-800ms)
    const delay = 300 + Math.floor(Math.random() * 500)
    const runId = app.nextRunId()
    setTimeout(() => {
      this.setData({
        submitLabel: '已提交',
        submitStatus: '申请已保存',
        savedRunId: String(runId)
      })
      console.log('aftersales:run_id:' + runId)
    }, delay)
  },

  onUpload() {
    console.log('aftersales:upload triggered')
  },

  // Reset for deterministic test runs
  reset() {
    this.setData({
      reasonValue: '',
      contactValue: '',
      canSubmit: false,
      submitLabel: '提交申请',
      submitStatus: '待填写',
      keyboardHideCount: 0,
      savedRunId: ''
    })
  }
})
