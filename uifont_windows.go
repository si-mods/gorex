package main

// uiFont is the family of the app's own text. Segoe UI has no Chinese, and
// the system falls back to Microsoft YaHei, which has only Light, Regular
// and Bold: the weights between them do not show. Noto Sans SC, where the
// system has it, comes before that fallback; where it has not, mygo skips
// it and the text is as it was.
const uiFont = "system-ui, Noto Sans SC"
