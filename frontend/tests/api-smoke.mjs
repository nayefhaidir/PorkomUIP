import assert from "node:assert/strict";

const base = process.env.BASE_URL ?? "http://localhost:3000";
const response = await fetch(`${base}/api/health`);
assert.equal(response.status, 200, "health endpoint responds");
const unauthorized = await fetch(`${base}/api/meetings`);
assert.equal(unauthorized.status, 401, "private API rejects anonymous users");
const unauthorizedAvailability = await fetch(`${base}/api/availability`);
assert.equal(unauthorizedAvailability.status, 400, "public availability endpoint validates its required date range");

const login = await fetch(`${base}/api/auth/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ email: "admin@uip.local", password: process.env.DEMO_PASSWORD ?? "UIPDemo2026!" }) });
assert.equal(login.status, 200, "demo admin can log in");
const cookie = login.headers.get("set-cookie")?.split(";")[0];
assert.ok(cookie, "login creates an HTTP cookie session");
const requestWith = async (sessionCookie, path, options = {}) => fetch(`${base}${path}`, { ...options, headers: { Cookie: sessionCookie, ...(options.body instanceof FormData ? {} : { "Content-Type": "application/json" }), ...options.headers } });
const api = (path, options = {}) => requestWith(cookie, path, options);
const profile = await (await api("/api/auth/me")).json();
assert.equal(profile.email, "admin@uip.local", "profile endpoint returns the authenticated user");
const profileUpdate = await api("/api/auth/me", { method: "PATCH", body: JSON.stringify({ name: "Smoke Administrator" }) });
assert.equal((await profileUpdate.json()).name, "Smoke Administrator", "users can update their profile name");
await api("/api/auth/me", { method: "PATCH", body: JSON.stringify({ name: "Administrator UIP" }) });
await api("/api/settings", { method: "PATCH", body: JSON.stringify({ allowSelfApproval: false }) });
const departments = await (await api("/api/departments")).json();
const room = await (await api("/api/rooms?active=true&pageSize=100")).json();
assert.ok(departments.length && room.items.length, "seed data exists");
const smokeSuffix = Date.now().toString().slice(-6);
const departmentCreate = await api("/api/departments", { method: "POST", body: JSON.stringify({ name: `Smoke Department ${smokeSuffix}`, shortName: `SM${smokeSuffix}`, color: "#39A7C8" }) });
assert.equal(departmentCreate.status, 201, "admin can create a department with its calendar color");
const smokeDepartment = await departmentCreate.json();
assert.equal(smokeDepartment.color, "#39A7C8");
const duplicateDepartmentColor = await api("/api/departments", { method: "POST", body: JSON.stringify({ name: `Duplicate Color ${smokeSuffix}`, shortName: `DC${smokeSuffix}`, color: "#39A7C8" }) });
assert.equal(duplicateDepartmentColor.status, 409, "departments cannot share the same calendar color");
const departmentColorUpdate = await api(`/api/departments/${smokeDepartment.id}`, { method: "PATCH", body: JSON.stringify({ color: "#4A82C8" }) });
assert.equal((await departmentColorUpdate.json()).color, "#4A82C8", "department calendar color can be updated");
assert.equal((await api(`/api/departments/${smokeDepartment.id}`, { method: "DELETE" })).status, 200, "unused test department can be deleted");
const newUserEmail = `smoke-${Date.now()}@uip.local`;
const newUserResponse = await api("/api/users", { method: "POST", body: JSON.stringify({ name: "Smoke User", email: newUserEmail, password: "TemporaryPass2026!", role: "PARTICIPANT", departmentId: departments[0].id }) });
assert.equal(newUserResponse.status, 201, `admin can create a user: ${await newUserResponse.clone().text()}`);
const newUser = await newUserResponse.json();
const listedUser = await (await api(`/api/users?q=${encodeURIComponent(newUserEmail)}`)).json();
assert.ok(listedUser.items.some(user => user.id === newUser.id), "new user immediately appears in user search");
const participantLogin = await fetch(`${base}/api/auth/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ email: newUserEmail, password: "TemporaryPass2026!" }) });
assert.equal(participantLogin.status, 200, "new participant can log in");
const participantCookie = participantLogin.headers.get("set-cookie")?.split(";")[0];
assert.ok(participantCookie);
const participantReport = await requestWith(participantCookie, "/api/reports/monthly?month=2026-10");
assert.equal(participantReport.status, 403, "ordinary participants cannot access monthly reports");
await requestWith(participantCookie, "/api/auth/logout", { method: "POST" });
assert.equal((await api(`/api/users/${newUser.id}`, { method: "PATCH", body: JSON.stringify({ isActive: false }) })).status, 200, "test user can be disabled after session use");
const startAt = new Date(Date.now() + (2 + Math.floor(Math.random() * 28)) * 24 * 60 * 60 * 1000); startAt.setUTCHours(7 + Math.floor(Math.random() * 10), 0, 0, 0);
const endAt = new Date(startAt.getTime() + 60 * 60 * 1000);
const payload = { title: `Smoke test ${Date.now()}`, description: "API integration test", startAt: startAt.toISOString(), endAt: endAt.toISOString(), roomId: "room-uip-1", departmentId: departments[0].id, expectedParticipants: 2, attendeeIds: [], manualGuests: [{ name: "Guest Participant", email: "guest@example.com", organization: "External" }] };
const injectedTitle = await api("/api/meetings", { method: "POST", body: JSON.stringify({ ...payload, title: `${payload.title}\r\nBcc: attacker@example.test` }) });
assert.equal(injectedTitle.status, 400, "meeting titles reject SMTP header line breaks");
const backdated = await api("/api/meetings", { method: "POST", body: JSON.stringify({ ...payload, title: `${payload.title} backdated`, startAt: new Date(Date.now() - 60 * 60 * 1000).toISOString(), endAt: new Date(Date.now() + 30 * 60 * 1000).toISOString() }) });
assert.equal(backdated.status, 400, "backdated meeting requests are rejected");
const createdResponse = await api("/api/meetings", { method: "POST", body: JSON.stringify(payload) });
assert.equal(createdResponse.status, 201, `meeting creation succeeds: ${await createdResponse.clone().text()}`);
const meeting = await createdResponse.json();
assert.equal(meeting.guests.length, 1, "manual participants are saved with the meeting");
const ownApproval = await api(`/api/meetings/${meeting.id}/workflow`, { method: "POST", body: JSON.stringify({ action: "approve" }) });
assert.equal(ownApproval.status, 403, "requester cannot approve their own meeting");
const approverLogin = await fetch(`${base}/api/auth/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ email: "approver@uip.local", password: process.env.DEMO_PASSWORD ?? "UIPDemo2026!" }) });
assert.equal(approverLogin.status, 200, "seeded approver can log in");
const approverCookie = approverLogin.headers.get("set-cookie")?.split(";")[0];
assert.ok(approverCookie);
const approverApi = (path, options = {}) => requestWith(approverCookie, path, options);
const approvalWithoutSupportingDocument = await approverApi(`/api/meetings/${meeting.id}/workflow`, { method: "POST", body: JSON.stringify({ action: "approve" }) });
assert.equal(approvalWithoutSupportingDocument.status, 200, "supporting documents are optional during approval");
const conflict = await api("/api/meetings", { method: "POST", body: JSON.stringify({ ...payload, title: `${payload.title} conflict` }) });
assert.equal(conflict.status, 409, "overlapping meeting is rejected");

const pdf = new FormData(); pdf.append("file", new Blob(["%PDF-1.4 smoke"], { type: "application/pdf" }), "agenda.pdf");
const uploaded = await api(`/api/meetings/${meeting.id}/documents`, { method: "POST", body: pdf });
assert.equal(uploaded.status, 201, `document upload succeeds: ${await uploaded.clone().text()}`);
const document = await uploaded.json();
const downloaded = await api(`/api/documents/${document.id}`);
assert.equal(downloaded.status, 200, "authenticated document download succeeds");
assert.match(downloaded.headers.get("content-disposition"), /attachment/);

const canceled = await api(`/api/meetings/${meeting.id}/workflow`, { method: "POST", body: JSON.stringify({ action: "cancel", reason: "smoke cleanup" }) });
assert.equal(canceled.status, 200, "organizer/admin can cancel approved meeting");

const nextMeeting = await api("/api/meetings", { method: "POST", body: JSON.stringify({ ...payload, title: `${payload.title} rejection` }) });
assert.equal(nextMeeting.status, 201, "canceled room slot can be scheduled again");
const rejected = await approverApi(`/api/meetings/${(await nextMeeting.json()).id}/workflow`, { method: "POST", body: JSON.stringify({ action: "reject", reason: "Smoke test rejection" }) });
assert.equal(rejected.status, 200, "approver can reject with a reason");

const raceStart = new Date(Date.now() + 14 * 24 * 60 * 60 * 1000 + Math.floor(Math.random() * 24 * 60) * 60 * 1000); raceStart.setUTCSeconds(0, 0);
const racePayload = { ...payload, title: `${payload.title} race`, startAt: raceStart.toISOString(), endAt: new Date(raceStart.getTime() + 60 * 60 * 1000).toISOString() };
const race = await Promise.all([api("/api/meetings", { method: "POST", body: JSON.stringify(racePayload) }), api("/api/meetings", { method: "POST", body: JSON.stringify({ ...racePayload, title: `${racePayload.title} concurrent` }) })]);
assert.deepEqual(race.map(r => r.status).sort(), [201, 409], "concurrent bookings allow one winner and reject the race loser");
const winner = await race.find(r => r.status === 201)?.json();
assert.ok(winner, "the winning concurrent meeting persists");
await api(`/api/meetings/${winner.id}/workflow`, { method: "POST", body: JSON.stringify({ action: "cancel", reason: "Smoke cleanup" }) });

const reportMonth = startAt.toISOString().slice(0, 7);
for (const format of ["csv", "xlsx", "pdf"]) {
  const exported = await api(`/api/reports/monthly/export?month=${reportMonth}&format=${format}`);
  assert.equal(exported.status, 200, `${format} export succeeds`);
  assert.ok(Number(exported.headers.get("content-length") ?? 1) > 0 || (await exported.arrayBuffer()).byteLength > 0);
}

const loggedOut = await api("/api/auth/logout", { method: "POST" });
assert.equal(loggedOut.status, 200, "logout revokes the session");
await approverApi("/api/auth/logout", { method: "POST" });
const rejectedSession = await fetch(`${base}/api/auth/me`, { headers: { Cookie: cookie } });
assert.equal(rejectedSession.status, 401, "revoked session cannot be reused");
console.log("API smoke test passed: auth, authorization, room availability, conflicting/concurrent bookings, documents, approval/rejection/cancellation, CSV/XLSX/PDF exports, logout.");
