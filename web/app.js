// Contest Scoring System — Vue 3 SPA (offline, embedded in Go binary)
const { createApp, ref, reactive, computed, onMounted, watch } = Vue;

async function api(path, opts = {}) {
  const res = await fetch('/api/v1' + path, { headers: { 'Content-Type': 'application/json' }, ...opts });
  let data = null;
  const ct = res.headers.get('content-type') || '';
  if (ct.includes('application/json')) data = await res.json();
  if (!res.ok) {
    const e = (data && data.error) || { code: 'HTTP_' + res.status, message: 'Lỗi ' + res.status };
    const err = new Error(e.message || e.code);
    err.code = e.code; err.details = e.details; err.status = res.status;
    throw err;
  }
  return data;
}
const j = (obj) => JSON.stringify(obj);
function uuid() {
  if (crypto.randomUUID) return crypto.randomUUID();
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, c => { const r = Math.random() * 16 | 0; return (c === 'x' ? r : (r & 0x3 | 0x8)).toString(16); });
}
// --- IndexedDB drafts (per judge x performance) ---
function idbOpen() {
  return new Promise((res, rej) => {
    const q = indexedDB.open('css-drafts', 1);
    q.onupgradeneeded = () => q.result.createObjectStore('drafts');
    q.onsuccess = () => res(q.result); q.onerror = () => rej(q.error);
  });
}
async function idbSet(key, val) { const db = await idbOpen(); return new Promise((res, rej) => { const tx = db.transaction('drafts', 'readwrite'); tx.objectStore('drafts').put(val, key); tx.oncomplete = res; tx.onerror = () => rej(tx.error); }); }
async function idbGet(key) { const db = await idbOpen(); return new Promise((res, rej) => { const q = db.transaction('drafts').objectStore('drafts').get(key); q.onsuccess = () => res(q.result || null); q.onerror = () => rej(q.error); }); }

const UI = {
  vi: { login: 'Đăng nhập', logout: 'Đăng xuất', username: 'Tên đăng nhập', password: 'Mật khẩu / PIN', submit: 'Gửi điểm', saveDraft: 'Lưu nháp', current: 'Đang thi', publicTitle: 'Màn hình khán giả' },
  en: { login: 'Sign in', logout: 'Sign out', username: 'Username', password: 'Password / PIN', submit: 'Submit scores', saveDraft: 'Save draft', current: 'Now performing', publicTitle: 'Public screen' }
};

createApp({
  setup() {
    const user = ref(null);
    const lang = ref(localStorage.getItem('css-lang') || 'vi');
    const err = ref(''); const ok = ref('');
    const loginForm = reactive({ username: '', password: '' });
    const contests = ref([]); const contest = ref(null);
    const rounds = ref([]); const teamsByRound = reactive({});
    const groups = ref([]); const totalMax = ref(0);
    const assignments = ref([]);
    const users = ref([]);
    const tab = ref('dash');
    const perfs = ref([]); const matrix = ref(null);
    const boards = reactive({}); const audit = ref([]);
    const backups = ref([]);
    const progress = ref(null); const myAssignments = ref([]);
    const publicState = ref(null);
    const timerInfo = reactive({});
    // forms
    const newContest = reactive({ name_vi: '', name_en: '', location: '' });
    const newRound = reactive({ name_vi: '', name_en: '' });
    const newTeam = reactive({ round_id: null, code: '', name_vi: '', name_en: '', organization: '' });
    const newGroup = reactive({ name_vi: '', name_en: '' });
    const newCrit = reactive({ group_id: null, name_vi: '', name_en: '', input_type: 'range', min_score: 0, max_score: 10, step: 0.5, weight: 1, is_admin_only: false });
    const newUser = reactive({ username: '', display_name: '', role: 'judge', password: '' });
    const newOption = reactive({ criterion_id: null, label_vi: '', label_en: '', score_value: 0 });
    // judge scoring
    const curPerf = ref(null); const formDetails = reactive({}); const formOption = reactive({});
    const formComment = ref(''); const scoreMeta = reactive({ uuid: '', version: 0, status: '', id: null });
    const syncStatus = ref('');
    const detailScore = ref(null);
    let ws = null; let pollTimer = null;

    const t = computed(() => UI[lang.value]);
    const pick = (o, base) => { if (!o) return ''; const en = o[base + '_en'], vi = o[base + '_vi']; return lang.value === 'en' ? (en || vi || '') : (vi || en || ''); };
    const fmt = (sec) => { sec = Math.max(0, Math.floor(sec || 0)); return String(Math.floor(sec / 60)).padStart(2, '0') + ':' + String(sec % 60).padStart(2, '0'); };
    function setLang(l) { lang.value = l; localStorage.setItem('css-lang', l); }
    function flash(e) { err.value = e.message || String(e); ok.value = ''; }
    function good(m) { ok.value = m; err.value = ''; }

    async function doLogin() {
      try { const d = await api('/auth/login', { method: 'POST', body: j(loginForm) }); user.value = d.user; good(''); await afterLogin(); }
      catch (e) { flash(e); }
    }
    async function doLogout() { try { await api('/auth/logout', { method: 'POST', body: '{}' }); } catch (e) {} user.value = null; if (ws) ws.close(); }
    async function changePw() {
      const cur = prompt('Mật khẩu hiện tại:'); if (!cur) return;
      const nw = prompt('Mật khẩu/PIN mới (ít nhất 4 ký tự):'); if (!nw) return;
      try { await api('/auth/change-password', { method: 'POST', body: j({ current: cur, new: nw }) }); good('Đã đổi mật khẩu/PIN.'); } catch (e) { flash(e); }
    }

    async function loadContests() { const d = await api('/contests'); contests.value = d.contests || []; }
    async function selectContest(c) {
      contest.value = c; tab.value = 'dash';
      await refreshContest();
    }
    async function refreshContest() {
      if (!contest.value) return;
      const id = contest.value.id;
      const d = await api('/contests/' + id); contest.value = d.contest;
      const r = await api('/contests/' + id + '/rounds'); rounds.value = r.rounds || [];
      for (const rd of rounds.value) { const td = await api('/rounds/' + rd.id + '/teams'); teamsByRound[rd.id] = td.teams || []; await loadBoard(rd.id); }
      const cr = await api('/contests/' + id + '/criteria'); groups.value = cr.groups || []; totalMax.value = cr.total_max_score || 0;
      const st = await api('/contests/' + id + '/state'); perfs.value = st.performances || [];
      if (user.value.role === 'admin') {
        const as = await api('/contests/' + id + '/assignments'); assignments.value = as.assignments || [];
        await loadMatrix(); await loadAudit(); await loadBackups();
      }
      if (user.value.role === 'mc') await loadPublic();
    }
    async function loadBoard(rid) { try { const d = await api('/rounds/' + rid + '/leaderboard'); boards[rid] = d.leaderboard || []; } catch (e) { boards[rid] = []; } }
    async function loadMatrix() { if (!contest.value) return; matrix.value = await api('/contests/' + contest.value.id + '/live-matrix'); }
    async function loadAudit() { if (!contest.value) return; const d = await api('/contests/' + contest.value.id + '/audit-logs?page_size=100'); audit.value = d.audit_logs || []; }
    async function loadBackups() { const d = await api('/backups'); backups.value = d.backups || []; }
    async function loadUsers() { const d = await api('/users'); users.value = d.users || []; }
    async function loadPublic() { if (!contest.value) { const cs = await api('/contests'); if (cs.contests && cs.contests.length) contest.value = cs.contests.find(c => c.status === 'active') || cs.contests[0]; } if (contest.value) publicState.value = await api('/contests/' + contest.value.id + '/public-state'); }
    async function loadJudge() {
      const p = await api('/judges/me/progress'); progress.value = p;
      const a = await api('/judges/me/assignments'); myAssignments.value = a.assignments || [];
      const cs = await api('/contests'); contests.value = cs.contests || [];
      const act = contests.value.find(c => c.status === 'active') || contests.value[0];
      if (act) { contest.value = act; const cr = await api('/contests/' + act.id + '/criteria'); groups.value = cr.groups || []; totalMax.value = cr.total_max_score || 0;
        const st = await api('/contests/' + act.id + '/state'); perfs.value = st.performances || [];
        const cur = perfs.value.find(p => p.status === 'performing');
        if (cur) await openPerf(cur.id, true);
      }
    }
    async function afterLogin() {
      connectWS();
      if (user.value.role === 'admin') { await loadContests(); await loadUsers(); }
      else if (user.value.role === 'judge') { await loadJudge(); }
      else { await loadPublic(); }
      if (pollTimer) clearInterval(pollTimer);
      pollTimer = setInterval(async () => {
        if (!user.value) return;
        try {
          if (user.value.role === 'mc') await loadPublic();
          else if (user.value.role === 'judge') { const p = await api('/judges/me/progress'); progress.value = p; if (contest.value) { const st = await api('/contests/' + contest.value.id + '/state'); perfs.value = st.performances || []; const cur = perfs.value.find(x => x.status === 'performing'); if (cur && (!curPerf.value || curPerf.value.id !== cur.id)) await openPerf(cur.id, true); } }
          else if (contest.value && tab.value === 'dash') { const st = await api('/contests/' + contest.value.id + '/state'); perfs.value = st.performances || []; await loadMatrix(); }
        } catch (e) {}
      }, 4000);
    }
    function connectWS() {
      try {
        ws = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws');
        ws.onopen = () => { ws.send(j({ type: 'client.heartbeat' })); setInterval(() => { if (ws && ws.readyState === 1) ws.send(j({ type: 'client.heartbeat' })); }, 10000); };
        ws.onmessage = async (ev) => {
          let m; try { m = JSON.parse(ev.data); } catch (e) { return; }
          if (m.type === 'timer.sync') { timerInfo[m.performance_id] = m; }
          if (['performance.started', 'performance.ended', 'performance.locked', 'performance.revealed', 'score.updated', 'leaderboard.updated', 'contest.state', 'penalty.updated'].includes(m.type)) {
            if (user.value && user.value.role === 'mc') await loadPublic();
            else if (user.value && user.value.role === 'judge' && m.type === 'performance.started') { await loadJudge(); }
            else if (contest.value && user.value && user.value.role === 'admin' && tab.value === 'dash') { const st = await api('/contests/' + contest.value.id + '/state'); perfs.value = st.performances || []; await loadMatrix(); for (const rd of rounds.value) await loadBoard(rd.id); }
          }
        };
      } catch (e) {}
    }
    // ---------- admin actions ----------
    async function createContest() { try { const d = await api('/contests', { method: 'POST', body: j(newContest) }); await loadContests(); await selectContest(d.contest); good('Đã tạo cuộc thi.'); } catch (e) { flash(e); } }
    async function patchContest(patch) { try { const d = await api('/contests/' + contest.value.id, { method: 'PATCH', body: j(patch) }); contest.value = d.contest; good('Đã lưu cấu hình.'); } catch (e) { flash(e); } }
    async function contestAction(action) {
      try {
        let body = {};
        if (action === 'unfinalize' || action === 'reset-scores') { const reason = action === 'reset-scores' ? 'confirm' : prompt('Lý do (bắt buộc):'); if (action === 'unfinalize' && !reason) return; body = action === 'reset-scores' ? { confirm: true } : { reason }; }
        if (action === 'reset-scores' && !confirm('Reset toàn bộ điểm của cuộc thi này?')) return;
        const d = await api('/contests/' + contest.value.id + '/' + action, { method: 'POST', body: j(body) });
        if (d.contest) contest.value = d.contest;
        if (d.error) throw new Error(d.error.message);
        good('Đã thực hiện: ' + action); await refreshContest(); await loadContests();
      } catch (e) { if (e.details && e.details.blockers) err.value = e.message + ' — ' + e.details.blockers.map(b => b.message).join('; '); else flash(e); }
    }
    async function createRound() { try { await api('/contests/' + contest.value.id + '/rounds', { method: 'POST', body: j(newRound) }); newRound.name_vi = ''; newRound.name_en = ''; await refreshContest(); } catch (e) { flash(e); } }
    async function createTeam(rid) { try { newTeam.round_id = rid; await api('/rounds/' + rid + '/teams', { method: 'POST', body: j(newTeam) }); Object.assign(newTeam, { code: '', name_vi: '', name_en: '', organization: '' }); await refreshContest(); } catch (e) { flash(e); } }
    async function importCSV(rid, ev) {
      const f = ev.target.files[0]; if (!f) return;
      const fd = new FormData(); fd.append('file', f);
      try { const res = await fetch('/api/v1/rounds/' + rid + '/teams/import', { method: 'POST', body: fd }); const d = await res.json(); if (!res.ok) throw new Error((d.error || {}).message || 'Import lỗi'); good('Import CSV: ' + d.imported + ' đội' + (d.errors && d.errors.length ? ('; lỗi: ' + d.errors.map(x => 'dòng ' + x.row + ': ' + x.message).join('; ')) : '')); await refreshContest(); } catch (e) { flash(e); }
      ev.target.value = '';
    }
    async function shuffle(rid) { try { await api('/rounds/' + rid + '/shuffle-order', { method: 'POST', body: '{}' }); await refreshContest(); } catch (e) { flash(e); } }
    async function createGroup() { try { await api('/contests/' + contest.value.id + '/criteria-groups', { method: 'POST', body: j(newGroup) }); newGroup.name_vi = ''; await refreshContest(); } catch (e) { flash(e); } }
    async function createCrit() { try { await api('/criteria-groups/' + newCrit.group_id + '/criteria', { method: 'POST', body: j(newCrit) }); await refreshContest(); good('Đã thêm tiêu chí.'); } catch (e) { flash(e); } }
    async function addOption() { try { await api('/criteria/' + newOption.criterion_id + '/options', { method: 'POST', body: j(newOption) }); await refreshContest(); } catch (e) { flash(e); } }
    const judges = computed(() => users.value.filter(u => u.role === 'judge'));
    function isAssigned(pid, jid) { return assignments.value.some(a => a.performance_id === pid && a.judge_id === jid); }
    function toggleAssign(pid, jid, ev) { if (ev.target.checked) assignments.value.push({ performance_id: pid, judge_id: jid }); else assignments.value = assignments.value.filter(a => !(a.performance_id === pid && a.judge_id === jid)); }
    function assignAll() { const out = []; for (const p of perfs.value) for (const jd of judges.value) out.push({ performance_id: p.id, judge_id: jd.id }); assignments.value = out; }
    async function saveAssignments() { try { await api('/contests/' + contest.value.id + '/assignments', { method: 'PUT', body: j({ assignments: assignments.value }) }); good('Đã lưu phân công.'); await refreshContest(); } catch (e) { flash(e); } }
    async function perfAction(p, action) {
      try {
        let body = {};
        if (['lock', 'unlock', 'reject-penalty'].includes(action)) { const reason = prompt('Lý do (bắt buộc cho thao tác này; để trống nếu hệ thống không yêu cầu):'); body = { reason: reason || '' }; }
        if (action === 'approve-penalty') { const v = prompt('Mức trừ áp dụng (để trống = giữ mức đề xuất ' + (p.penalty_proposed ?? '') + '):'); if (v) { const reason = prompt('Lý do sửa mức trừ:'); body = { penalty_applied: parseFloat(v), reason: reason || '' }; } }
        await api('/performances/' + p.id + '/' + action, { method: 'POST', body: j(body) });
        good('Đã thực hiện ' + action + ' cho ' + p.team_code); await refreshContest();
      } catch (e) { flash(e); }
    }
    async function viewScores(p) { try { const d = await api('/performances/' + p.id + '/scores'); detailScore.value = { perf: p, scores: d.scores || [] }; } catch (e) { flash(e); } }
    async function override(sc, crit) {
      const v = prompt('Điểm mới cho tiêu chí (hiện: ' + crit.raw_score + '):'); if (v === null) return;
      const reason = prompt('Lý do override (bắt buộc):'); if (!reason) return;
      try { await api('/scores/' + sc.id + '/override', { method: 'POST', body: j({ details: [{ criterion_id: crit.criterion_id, raw_score: parseFloat(v) }], reason }) }); good('Đã override.'); detailScore.value = null; await refreshContest(); await loadAudit(); } catch (e) { flash(e); }
    }
    async function adminSheet(p) {
      const groupsA = groups.value; let crit = null;
      for (const g of groupsA) for (const c of g.criteria) if (c.is_admin_only && !c.is_penalty_criterion) crit = c;
      if (!crit) { err.value = 'Chưa có tiêu chí Admin-Only thường.'; return; }
      const v = prompt('Nhập điểm Admin-Only cho tiêu chí "' + crit.name_vi + '" (âm = trừ, tối đa ±' + crit.max_score + '):'); if (v === null) return;
      const comment = prompt('Ghi chú (bắt buộc):'); if (!comment) return;
      try { await api('/performances/' + p.id + '/admin-sheet', { method: 'PUT', body: j({ details: [{ criterion_id: crit.id, raw_score: parseFloat(v) }], comment }) }); good('Đã lưu điểm Admin-Only.'); await refreshContest(); } catch (e) { flash(e); }
    }
    async function tieBreak(rid) {
      const board = boards[rid] || []; const tied = board.filter(x => x.tied);
      if (!tied.length) { err.value = 'Không có đội đồng điểm trong vòng này.'; return; }
      const orderTxt = prompt('Nhập thứ tự team_id của nhóm đồng điểm, cách nhau bởi dấu phẩy. Nhóm đồng điểm: ' + tied.map(x => x.team_id + ' (' + x.team_code + ')').join(', '));
      if (!orderTxt) return;
      const reason = prompt('Lý do chốt (bắt buộc):'); if (!reason) return;
      try { await api('/rounds/' + rid + '/tie-break', { method: 'POST', body: j({ ordering: orderTxt.split(',').map(x => parseInt(x.trim(), 10)).filter(x => !isNaN(x)), reason }) }); await loadBoard(rid); good('Đã chốt thứ hạng thủ công.'); } catch (e) { flash(e); }
    }
    async function lbReveal(rd) { try { await api('/rounds/' + rd.id + '/leaderboard-reveal', { method: 'POST', body: j({ revealed: !rd.leaderboard_revealed }) }); await refreshContest(); } catch (e) { flash(e); } }
    async function createUser() { try { await api('/users', { method: 'POST', body: j(newUser) }); Object.assign(newUser, { username: '', display_name: '', password: '' }); await loadUsers(); good('Đã tạo tài khoản.'); } catch (e) { flash(e); } }
    async function resetCred(u) { const v = prompt('Mật khẩu/PIN mới cho ' + u.username + ' (để trống = server tự sinh):'); try { const d = await api('/users/' + u.id + '/reset-credential', { method: 'POST', body: j({ type: 'password', value: v || '' }) }); good('Đã reset.' + (d.generated_value ? ' Giá trị mới (chỉ hiện một lần): ' + d.generated_value : '')); } catch (e) { flash(e); } }
    async function patchUser(u, patch) { try { await api('/users/' + u.id, { method: 'PATCH', body: j(patch) }); await loadUsers(); } catch (e) { flash(e); } }
    async function unlockUser(u) { try { await api('/users/' + u.id + '/unlock', { method: 'POST', body: '{}' }); good('Đã mở khóa đăng nhập.'); } catch (e) { flash(e); } }
    async function createBackup() { try { await api('/backups', { method: 'POST', body: j({ contest_id: contest.value ? contest.value.id : 0 }) }); await loadBackups(); good('Đã tạo snapshot backup.'); } catch (e) { flash(e); } }
    async function restoreUpload(ev) {
      const f = ev.target.files[0]; if (!f) return; if (!confirm('Restore sẽ thay dữ liệu hiện tại (hệ thống tự tạo snapshot pre_restore trước). Tiếp tục?')) { ev.target.value = ''; return; }
      const fd = new FormData(); fd.append('file', f);
      try { const res = await fetch('/api/v1/backups/upload-restore', { method: 'POST', body: fd }); const d = await res.json(); if (!res.ok) throw new Error((d.error || {}).message || 'Restore lỗi'); good('Restore xong. Tải lại dữ liệu…'); await refreshContest(); } catch (e) { flash(e); }
      ev.target.value = '';
    }
    // ---------- judge ----------
    const assignedPerfIds = computed(() => new Set(myAssignments.value.map(a => a.performance_id)));
    function flatCriteria(includeAdmin) { const out = []; for (const g of groups.value) for (const c of g.criteria) if (includeAdmin || !c.is_admin_only) out.push({ ...c, group_name: g.name_vi }); return out; }
    async function openPerf(pid, silent) {
      const p = perfs.value.find(x => x.id === pid) || (progress.value && progress.value.items.find(i => i.performance_id === pid) ? { id: pid, status: (progress.value.items.find(i => i.performance_id === pid) || {}).performance_status } : null);
      if (!p) return;
      curPerf.value = p;
      for (const k of Object.keys(formDetails)) delete formDetails[k];
      for (const k of Object.keys(formOption)) delete formOption[k];
      scoreMeta.uuid = ''; scoreMeta.version = 0; scoreMeta.status = ''; scoreMeta.id = null; formComment.value = '';
      // server score
      try { const d = await api('/performances/' + pid + '/my-score'); if (d.score) { scoreMeta.uuid = d.score.client_uuid; scoreMeta.version = d.score.version; scoreMeta.status = d.score.status; scoreMeta.id = d.score.id; formComment.value = d.score.comment || ''; for (const dt of d.score.details) { formDetails[dt.criterion_id] = dt.raw_score; if (dt.option_id) formOption[dt.criterion_id] = dt.option_id; } } } catch (e) {}
      // local draft (newer local draft wins for display when server is draft/none)
      const local = await idbGet(user.value.id + ':' + pid).catch(() => null);
      if (local && (!scoreMeta.status || scoreMeta.status === 'draft')) {
        scoreMeta.uuid = local.uuid || scoreMeta.uuid;
        for (const dt of (local.details || [])) { formDetails[dt.criterion_id] = dt.raw_score; if (dt.option_id) formOption[dt.criterion_id] = dt.option_id; }
        if (local.comment) formComment.value = local.comment;
        syncStatus.value = 'Đã lưu trên máy này (IndexedDB)';
      }
      if (!scoreMeta.uuid) scoreMeta.uuid = uuid();
      if (!silent) good('');
    }
    function collectDetails() {
      const out = [];
      for (const c of flatCriteria(false)) {
        const v = formDetails[c.id];
        if (v === undefined || v === null || v === '') continue;
        const d = { criterion_id: c.id, raw_score: parseFloat(v) };
        if (c.input_type === 'discrete' && formOption[c.id]) d.option_id = formOption[c.id];
        out.push(d);
      }
      return out;
    }
    async function saveDraftLocal() {
      const details = collectDetails();
      await idbSet(user.value.id + ':' + curPerf.value.id, { uuid: scoreMeta.uuid, details, comment: formComment.value, ts: Date.now() });
    }
    async function judgeSave(submit) {
      if (!curPerf.value) return;
      await saveDraftLocal();
      const details = collectDetails();
      try {
        const d = await api('/scores/by-client-uuid/' + scoreMeta.uuid, { method: 'PUT', body: j({ performance_id: curPerf.value.id, base_version: scoreMeta.version || 1, details, comment: formComment.value, submit: !!submit, reason: '' }) });
        scoreMeta.version = d.score.version; scoreMeta.status = d.score.status; scoreMeta.id = d.score.id;
        syncStatus.value = submit ? 'Đã nộp' : 'Đã đồng bộ';
        good(submit ? 'Đã nộp điểm.' : 'Đã lưu nháp lên server.');
        const p = await api('/judges/me/progress'); progress.value = p;
      } catch (e) {
        if (e.code === 'SCORE_VERSION_CONFLICT') { syncStatus.value = 'Có xung đột — server đã có bản khác. Nháp của bạn vẫn giữ trên máy này để đối chiếu.'; }
        else syncStatus.value = 'Đã lưu trên máy này (chưa đồng bộ được)';
        flash(e);
      }
    }
    async function judgeEditSubmitted() {
      const reason = prompt('Lý do sửa điểm đã nộp (bắt buộc):'); if (!reason) return;
      try { const d = await api('/scores/' + scoreMeta.id, { method: 'PATCH', body: j({ performance_id: curPerf.value.id, details: collectDetails(), reason }) }); scoreMeta.version = d.score.version; good('Đã sửa điểm (đã ghi audit).'); } catch (e) { flash(e); }
    }
    function onDetailChange() { saveDraftLocal(); syncStatus.value = 'Đã lưu trên máy này (IndexedDB)'; if (ws && ws.readyState === 1 && curPerf.value) ws.send(j({ type: 'score.draft_ping', performance_id: curPerf.value.id })); }

    onMounted(async () => {
      try { const d = await api('/auth/me'); user.value = d.user; await afterLogin(); } catch (e) {}
    });

    return { user, lang, t, err, ok, loginForm, doLogin, doLogout, changePw, setLang, pick, fmt, contests, contest, rounds, teamsByRound, groups, totalMax, assignments, users, tab, perfs, matrix, boards, audit, backups, progress, myAssignments, publicState, timerInfo, newContest, newRound, newTeam, newGroup, newCrit, newUser, newOption, curPerf, formDetails, formOption, formComment, scoreMeta, syncStatus, detailScore, createContest, patchContest, contestAction, selectContest, refreshContest, createRound, createTeam, importCSV, shuffle, createGroup, createCrit, addOption, judges, isAssigned, toggleAssign, assignAll, saveAssignments, perfAction, viewScores, override, adminSheet, tieBreak, lbReveal, createUser, resetCred, patchUser, unlockUser, createBackup, restoreUpload, assignedPerfIds, flatCriteria, openPerf, judgeSave, judgeEditSubmitted, onDetailChange, loadBoard };
  },
  template: `
  <div>
    <header class="top">
      <b>🏆 Contest Scoring System</b>
      <span v-if="user">{{ user.display_name }} · <span class="badge">{{ user.role }}</span></span>
      <span class="flex">
        <button class="secondary" @click="setLang(lang==='vi'?'en':'vi')">{{ lang==='vi' ? 'VI | EN' : 'EN | VI' }}</button>
        <button v-if="user" class="secondary" @click="changePw">Đổi mật khẩu</button>
        <button v-if="user" class="secondary" @click="doLogout">{{ t.logout }}</button>
      </span>
    </header>
    <main>
      <div v-if="err" class="err">{{ err }}</div>
      <div v-if="ok" class="ok">{{ ok }}</div>

      <div v-if="!user" class="card login">
        <h1>{{ t.login }}</h1>
        <p class="muted">Mẫu: admin/admin123 · judge1/1111 · mc/mc123</p>
        <input v-model="loginForm.username" :placeholder="t.username" style="width:100%"><br>
        <input v-model="loginForm.password" type="password" :placeholder="t.password" style="width:100%" @keyup.enter="doLogin"><br>
        <button style="width:100%" @click="doLogin">{{ t.login }}</button>
      </div>

      <!-- ADMIN -->
      <div v-else-if="user.role==='admin'">
        <div class="card">
          <h2>Cuộc thi</h2>
          <div class="flex">
            <select v-model="contest" @change="selectContest(contest)"><option :value="null">— Chọn cuộc thi —</option><option v-for="c in contests" :key="c.id" :value="c">{{ pick(c,'name') }} ({{ c.status }})</option></select>
            <input v-model="newContest.name_vi" placeholder="Tên cuộc thi mới (VI)">
            <input v-model="newContest.name_en" placeholder="Name (EN)">
            <input v-model="newContest.location" placeholder="Địa điểm">
            <button @click="createContest">+ Tạo cuộc thi</button>
          </div>
          <div v-if="contest">
            <p><b>{{ pick(contest,'name') }}</b> · trạng thái: <span class="badge">{{ contest.status }}</span> <span v-if="contest.is_finalized" class="badge">ĐÃ CHỐT SỔ</span> · join code: <b>{{ contest.join_code }}</b> · tổng điểm tối đa: <b>{{ totalMax }}</b></p>
            <div class="flex">
              <label><input type="checkbox" :checked="contest.allow_judge_edit_after_submit" @change="patchContest({allow_judge_edit_after_submit:$event.target.checked})"> Cho sửa sau nộp</label>
              <label><input type="checkbox" :checked="contest.drop_highest_lowest" @change="patchContest({drop_highest_lowest:$event.target.checked})"> Bỏ cao/thấp nhất</label>
              <label>Ngưỡng bỏ (≥4): <input type="number" style="width:70px" :value="contest.min_judges_for_trim" @change="patchContest({min_judges_for_trim:parseInt($event.target.value)})"></label>
              <label>Tiêu chí tie-break: <select @change="patchContest({tie_breaker_criterion_id:$event.target.value?parseInt($event.target.value):null})"><option value="">— không —</option><template v-for="g in groups"><option v-for="c in g.criteria" :key="c.id" :value="c.id" :selected="contest.tie_breaker_criterion_id===c.id">{{ c.name_vi }}</option></template></select></label>
            </div>
            <div class="flex">
              <button v-if="contest.status==='pending'" @click="contestAction('activate')">▶ Kích hoạt (checklist)</button>
              <button v-if="contest.status==='active'" class="warn" @click="contestAction('complete')">Kết thúc cuộc thi</button>
              <button v-if="contest.status==='completed' && !contest.is_finalized" @click="contestAction('finalize')">Chốt sổ</button>
              <button v-if="contest.is_finalized" class="danger" @click="contestAction('unfinalize')">Mở chốt sổ</button>
              <button class="secondary" @click="contestAction('clone')">Nhân bản (Clone)</button>
              <button class="danger" @click="contestAction('reset-scores')">Reset điểm</button>
              <a :href="'/api/v1/contests/'+contest.id+'/export.xlsx'"><button class="secondary">⬇ Xuất Excel</button></a>
              <button class="secondary" @click="createBackup">Tạo backup</button>
            </div>
            <div class="flex">
              <span class="muted">Backup: <span v-for="b in backups" :key="b.id" style="margin-right:.6rem">{{ b.file_name }} ({{ b.kind }}) <a :href="'/api/v1/backups/'+b.id+'/download'">tải</a></span></span>
              <label class="muted">Restore từ file: <input type="file" accept=".db" @change="restoreUpload"></label>
            </div>
          </div>
        </div>

        <div v-if="contest" class="tabs">
          <button :class="{on:tab==='dash'}" @click="tab='dash'">Điều hành</button>
          <button :class="{on:tab==='setup'}" @click="tab='setup'">Cấu hình (vòng/đội/tiêu chí)</button>
          <button :class="{on:tab==='assign'}" @click="tab='assign'">Phân công</button>
          <button :class="{on:tab==='users'}" @click="tab='users'">Tài khoản</button>
          <button :class="{on:tab==='audit'}" @click="tab='audit'">Audit log</button>
        </div>

        <div v-if="contest && tab==='dash'" class="card">
          <h2>Điều khiển luồng & Bảng xếp hạng</h2>
          <table><thead><tr><th>SBD</th><th>Đội</th><th>Trạng thái</th><th>Timer</th><th>Trừ giờ</th><th>Thao tác</th></tr></thead>
          <tbody><tr v-for="p in perfs" :key="p.id">
            <td>{{ p.team_code }}</td><td>{{ pick(p,'team_name') }}</td><td><span class="badge">{{ p.status }}</span> {{ p.timer_status }}</td>
            <td>{{ fmt((timerInfo[p.id]&&timerInfo[p.id].elapsed_sec)!=null?timerInfo[p.id].elapsed_sec:p.elapsed_sec) }} / {{ fmt(p.standard_duration_sec) }}<span v-if="(timerInfo[p.id]&&timerInfo[p.id].overtime_sec)||p.overtime_sec"> · LỐ {{ fmt((timerInfo[p.id]&&timerInfo[p.id].overtime_sec)||p.overtime_sec) }}</span></td>
            <td>{{ p.penalty_status }}<span v-if="p.penalty_proposed!=null"> · đề xuất {{ p.penalty_proposed }}</span><span v-if="p.penalty_applied!=null"> · đã áp {{ p.penalty_applied }}</span></td>
            <td>
              <button v-if="p.status==='pending'" @click="perfAction(p,'start')">Bắt đầu thi</button>
              <button v-if="p.status==='performing' && p.timer_status!=='paused'" @click="perfAction(p,'pause')">Tạm dừng</button>
              <button v-if="p.status==='performing' && p.timer_status==='paused'" @click="perfAction(p,'resume')">Tiếp tục</button>
              <button v-if="p.status==='performing'" class="warn" @click="perfAction(p,'end')">Kết thúc phần thi</button>
              <button v-if="p.penalty_status==='proposed'" @click="perfAction(p,'approve-penalty')">Duyệt trừ giờ</button>
              <button v-if="p.penalty_status==='proposed'" class="danger" @click="perfAction(p,'reject-penalty')">Từ chối trừ</button>
              <button v-if="p.status==='scoring'" @click="perfAction(p,'lock')">Khóa điểm</button>
              <button v-if="p.status==='locked'" class="warn" @click="perfAction(p,'unlock')">Mở khóa</button>
              <button v-if="p.status==='locked'" @click="perfAction(p,'reveal')">Công bố</button>
              <button v-if="p.status==='revealed'" class="warn" @click="perfAction(p,'unreveal')">Thu hồi công bố</button>
              <button class="secondary" @click="viewScores(p)">Xem phiếu</button>
              <button class="secondary" @click="adminSheet(p)">Điểm Admin-Only</button>
            </td></tr></tbody></table>

          <div v-if="detailScore" class="card">
            <h3>Phiếu điểm — {{ detailScore.perf.team_code }} <button class="secondary" @click="detailScore=null">Đóng</button></h3>
            <div v-for="sc in detailScore.scores" :key="sc.id">
              <p><b>{{ sc.is_admin_sheet ? 'Admin-Only' : ('Giám khảo #' + (sc.judge_id||'')) }}</b> · {{ sc.status }} · tổng {{ sc.total }}</p>
              <table><thead><tr><th>Tiêu chí</th><th>Điểm thô</th><th></th></tr></thead><tbody>
              <tr v-for="d in sc.details" :key="d.criterion_id"><td>{{ d.criterion_id }}</td><td>{{ d.raw_score }}</td><td><button class="warn" @click="override(sc,d)">Override</button></td></tr>
              </tbody></table>
            </div>
          </div>

          <h3>Live Matrix (đội × giám khảo)</h3>
          <table v-if="matrix"><thead><tr><th>Đội</th><th v-for="jd in matrix.judges" :key="jd.id">{{ jd.display_name }} {{ jd.online ? '🟢' : '⚪' }}</th></tr></thead>
          <tbody><tr v-for="row in matrix.rows" :key="row.performance.id"><td>{{ row.performance.team_code }}</td><td v-for="cell in row.cells" :key="cell.judge_id"><span class="badge" :class="cell.state">{{ cell.state }}</span></td></tr></tbody></table>

          <div v-for="rd in rounds" :key="rd.id">
            <h3>Bảng xếp hạng — {{ pick(rd,'name') }} <button class="secondary" @click="lbReveal(rd)">{{ rd.leaderboard_revealed ? 'Tắt reveal BXH' : 'Reveal BXH (Public)' }}</button> <button class="secondary" @click="tieBreak(rd.id)">Chốt thủ công đồng điểm</button></h3>
            <table><thead><tr><th>Hạng</th><th>SBD</th><th>Đội</th><th>ĐGK TB</th><th>Admin-Only</th><th>Điểm cuối</th><th>Ghi chú</th></tr></thead>
            <tbody><tr v-for="x in (boards[rd.id]||[])" :key="x.team_id" :class="{tie:x.tied}"><td>{{ x.rank }}</td><td>{{ x.team_code }}</td><td>{{ pick(x,'team_name') }}</td><td>{{ x.judge_average }}</td><td>{{ x.admin_only_total }}</td><td><b>{{ x.final_score }}</b></td><td class="muted">{{ x.trim_note || '' }}{{ x.manual_rank ? ' · Admin chốt' : '' }}{{ x.tied ? ' · ĐỒNG ĐIỂM' : '' }}</td></tr></tbody></table>
          </div>
        </div>

        <div v-if="contest && tab==='setup'" class="card">
          <h2>Vòng thi</h2>
          <div class="flex"><input v-model="newRound.name_vi" placeholder="Tên vòng (VI)"><input v-model="newRound.name_en" placeholder="Round (EN)"><button @click="createRound">+ Thêm vòng</button></div>
          <div v-for="rd in rounds" :key="rd.id" class="card">
            <h3>{{ pick(rd,'name') }}</h3>
            <div class="flex"><input v-model="newTeam.code" placeholder="SBD"><input v-model="newTeam.name_vi" placeholder="Tên đội (VI)"><input v-model="newTeam.name_en" placeholder="Team (EN)"><input v-model="newTeam.organization" placeholder="Đơn vị"><button @click="createTeam(rd.id)">+ Thêm đội</button>
            <label class="muted">Import CSV (code,name_vi,name_en,organization,sort_order): <input type="file" accept=".csv" @change="importCSV(rd.id,$event)"></label>
            <button class="secondary" @click="shuffle(rd.id)">🎲 Bốc thăm thứ tự</button></div>
            <table><thead><tr><th>SBD</th><th>Đội</th><th>Đơn vị</th><th>Thứ tự</th><th>Phần thi</th></tr></thead><tbody>
            <tr v-for="tm in (teamsByRound[rd.id]||[])" :key="tm.id"><td>{{ tm.code }}</td><td>{{ pick(tm,'name') }}</td><td>{{ tm.organization }}</td><td>{{ tm.sort_order }}</td><td>{{ tm.performance_status }}</td></tr></tbody></table>
          </div>
          <h2>Tiêu chí — tổng điểm tối đa (không tính Admin-Only): {{ totalMax }}</h2>
          <div class="flex"><input v-model="newGroup.name_vi" placeholder="Tên nhóm (VI)"><input v-model="newGroup.name_en" placeholder="Group (EN)"><button @click="createGroup">+ Thêm nhóm</button></div>
          <div v-for="g in groups" :key="g.id">
            <h3>{{ pick(g,'name') }}</h3>
            <table><thead><tr><th>Tiêu chí</th><th>Loại</th><th>Khoảng</th><th>Step</th><th>Weight</th><th>Admin-Only</th><th>Options</th></tr></thead><tbody>
            <tr v-for="c in g.criteria" :key="c.id"><td>{{ pick(c,'name') }}</td><td>{{ c.input_type }}</td><td>{{ c.min_score }}–{{ c.max_score }}</td><td>{{ c.step }}</td><td>×{{ c.weight }}</td><td>{{ c.is_admin_only ? '✓' : '' }}{{ c.is_penalty_criterion ? ' (trừ giờ)' : '' }}</td><td>{{ (c.options||[]).map(o=>o.label_vi+'='+o.score_value).join(', ') }}</td></tr></tbody></table>
          </div>
          <div class="flex">
            <select v-model="newCrit.group_id"><option :value="null">— Nhóm —</option><option v-for="g in groups" :key="g.id" :value="g.id">{{ g.name_vi }}</option></select>
            <input v-model="newCrit.name_vi" placeholder="Tên tiêu chí (VI)"><input v-model="newCrit.name_en" placeholder="Criterion (EN)">
            <select v-model="newCrit.input_type"><option value="range">range</option><option value="discrete">discrete</option></select>
            <input type="number" step="0.5" v-model.number="newCrit.min_score" placeholder="min" style="width:80px">
            <input type="number" step="0.5" v-model.number="newCrit.max_score" placeholder="max" style="width:80px">
            <input type="number" step="0.25" v-model.number="newCrit.step" placeholder="step" style="width:80px">
            <input type="number" step="0.5" v-model.number="newCrit.weight" placeholder="weight" style="width:80px">
            <label><input type="checkbox" v-model="newCrit.is_admin_only"> Admin-Only</label>
            <button @click="createCrit">+ Thêm tiêu chí</button>
          </div>
          <div class="flex">
            <select v-model="newOption.criterion_id"><option :value="null">— Tiêu chí discrete —</option><template v-for="g in groups"><option v-for="c in g.criteria.filter(x=>x.input_type==='discrete')" :key="c.id" :value="c.id">{{ c.name_vi }}</option></template></select>
            <input v-model="newOption.label_vi" placeholder="Nhãn (VI)"><input v-model="newOption.label_en" placeholder="Label (EN)">
            <input type="number" step="0.5" v-model.number="newOption.score_value" placeholder="Điểm" style="width:90px">
            <button @click="addOption">+ Thêm mức</button>
          </div>
        </div>

        <div v-if="contest && tab==='assign'" class="card">
          <h2>Ma trận phân công</h2>
          <button @click="assignAll">Gán tất cả (All-to-All)</button> <button @click="saveAssignments">Lưu phân công</button>
          <table><thead><tr><th>Đội</th><th v-for="jd in judges" :key="jd.id">{{ jd.display_name }}</th></tr></thead>
          <tbody><tr v-for="p in perfs" :key="p.id"><td>{{ p.team_code }} — {{ pick(p,'team_name') }}</td><td v-for="jd in judges" :key="jd.id"><input type="checkbox" :checked="isAssigned(p.id,jd.id)" @change="toggleAssign(p.id,jd.id,$event)"></td></tr></tbody></table>
        </div>

        <div v-if="tab==='users'" class="card">
          <h2>Tài khoản</h2>
          <div class="flex"><input v-model="newUser.username" placeholder="username"><input v-model="newUser.display_name" placeholder="Tên hiển thị"><select v-model="newUser.role"><option>judge</option><option>admin</option><option>mc</option></select><input v-model="newUser.password" placeholder="Mật khẩu/PIN đầu"><button @click="createUser">+ Tạo</button></div>
          <table><thead><tr><th>Username</th><th>Tên</th><th>Role</th><th>Hoạt động</th><th></th></tr></thead><tbody>
          <tr v-for="x in users" :key="x.id"><td>{{ x.username }}</td><td>{{ x.display_name }}</td><td>{{ x.role }}</td><td>{{ x.is_active ? '✓' : '✗' }}</td>
          <td><button class="secondary" @click="resetCred(x)">Reset mật khẩu/PIN</button><button class="secondary" @click="unlockUser(x)">Mở khóa ĐN</button><button class="warn" @click="patchUser(x,{is_active:!x.is_active})">{{ x.is_active ? 'Vô hiệu hóa' : 'Kích hoạt lại' }}</button></td></tr></tbody></table>
        </div>

        <div v-if="contest && tab==='audit'" class="card">
          <h2>Audit log (chỉ thêm, không sửa/xóa)</h2>
          <table><thead><tr><th>ID</th><th>Người</th><th>Hành động</th><th>Entity</th><th>Cũ → Mới</th><th>Lý do</th><th>Thời gian</th></tr></thead><tbody>
          <tr v-for="a in audit" :key="a.id"><td>{{ a.id }}</td><td>{{ a.actor_id }}</td><td>{{ a.action }}</td><td>{{ a.entity_type }}#{{ a.entity_id }}</td><td>{{ a.old_value }} → {{ a.new_value }}</td><td>{{ a.reason }}</td><td>{{ a.created_at }}</td></tr></tbody></table>
        </div>
      </div>

      <!-- JUDGE -->
      <div v-else-if="user.role==='judge'">
        <div class="card" v-if="progress">
          <h2>Tiến độ của tôi — Đã nộp: {{ progress.submitted }}/{{ progress.total }} đội</h2>
          <div class="flex"><button v-for="it in progress.items" :key="it.performance_id" :class="{secondary:true}" @click="openPerf(it.performance_id)">
            {{ it.team_code }} · <span class="badge" :class="it.my_status">{{ it.my_status === 'submitted' ? 'Đã nộp' : (it.my_status === 'draft' ? 'Đang chấm dở' : 'Chưa chấm') }}</span></button></div>
        </div>
        <div class="card" v-if="curPerf">
          <h2>{{ curPerf.team_code }} — {{ curPerf.team_name_vi }} <span class="badge">{{ curPerf.status }}</span></h2>
          <p class="muted">Trạng thái đồng bộ: <b>{{ syncStatus || scoreMeta.status || '—' }}</b> · version: {{ scoreMeta.version }}</p>
          <div v-if="assignedPerfIds.has(curPerf.id)">
            <div v-for="c in flatCriteria(false)" :key="c.id" class="card">
              <b>{{ pick(c,'name') }}</b> <span class="muted">({{ c.group_name }}) · tối đa {{ c.max_score }}<span v-if="c.weight!==1"> · ×{{ c.weight }}</span></span>
              <div v-if="c.input_type==='range'" class="flex">
                <input type="range" :min="c.min_score" :max="c.max_score" :step="c.step" v-model.number="formDetails[c.id]" @input="onDetailChange">
                <input type="number" :min="c.min_score" :max="c.max_score" :step="c.step" v-model.number="formDetails[c.id]" @change="onDetailChange" style="width:100px">
              </div>
              <div v-else class="flex">
                <button v-for="o in c.options" :key="o.id" class="opt" :class="{sel: formOption[c.id]===o.id}" @click="formOption[c.id]=o.id; formDetails[c.id]=o.score_value; onDetailChange()">{{ pick(o,'label') }} = {{ o.score_value }}</button>
              </div>
            </div>
            <input v-model="formComment" placeholder="Ghi chú phiếu điểm" style="width:100%" @change="onDetailChange">
            <div class="flex">
              <button class="secondary" @click="judgeSave(false)">{{ t.saveDraft }}</button>
              <button @click="judgeSave(true)">{{ t.submit }}</button>
              <button v-if="scoreMeta.status==='submitted'" class="warn" @click="judgeEditSubmitted">Sửa điểm đã nộp (cần lý do)</button>
            </div>
          </div>
          <p v-else>Bạn không được phân công chấm đội này — chỉ xem thông tin (read-only).</p>
        </div>
        <p class="muted" v-if="!curPerf">Chưa có phần thi nào đang diễn ra. Khi Admin bắt đầu thi, form chấm sẽ tự mở ở đây.</p>
      </div>

      <!-- MC / PUBLIC -->
      <div v-else-if="user.role==='mc'">
        <div class="card center" v-if="publicState">
          <h1>{{ pick(publicState.contest,'name') }}</h1>
          <div v-if="publicState.current_performance">
            <p class="muted">{{ t.current }}</p>
            <h2 style="font-size:2rem">{{ publicState.current_performance.team_code }} — {{ pick(publicState.current_performance,'team_name') }}</h2>
            <div class="big">{{ fmt(((timerInfo[publicState.current_performance.id]||{}).overtime_sec>0) ? (timerInfo[publicState.current_performance.id]||{}).overtime_sec : ((timerInfo[publicState.current_performance.id]||{}).remaining_sec!=null ? (timerInfo[publicState.current_performance.id]).remaining_sec : publicState.current_performance.remaining_sec)) }}</div>
            <p v-if="((timerInfo[publicState.current_performance.id]||{}).overtime_sec>0)">⏰ ĐANG LỐ GIỜ</p>
          </div>
          <p v-else class="muted">Đang chờ phần thi tiếp theo…</p>
          <div v-if="publicState.revealed_results && publicState.revealed_results.length">
            <h2>🎉 Kết quả đã công bố</h2>
            <table><thead><tr><th>SBD</th><th>Đội</th><th>Điểm cuối</th></tr></thead><tbody>
            <tr v-for="x in publicState.revealed_results" :key="x.team_id"><td>{{ x.team_code }}</td><td>{{ pick(x,'team_name') }}</td><td><b>{{ x.final_score }}</b></td></tr></tbody></table>
          </div>
          <div v-for="(board, rid) in (publicState.leaderboards||{})" :key="rid">
            <h2>🏆 Bảng xếp hạng</h2>
            <table><thead><tr><th>Hạng</th><th>SBD</th><th>Đội</th><th>Điểm cuối</th></tr></thead><tbody>
            <tr v-for="x in board" :key="x.team_id"><td>{{ x.rank }}</td><td>{{ x.team_code }}</td><td>{{ pick(x,'team_name') }}</td><td><b>{{ x.final_score }}</b></td></tr></tbody></table>
          </div>
        </div>
      </div>
    </main>
  </div>`
}).mount('#app');
