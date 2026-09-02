import assert from 'node:assert/strict';
import { chromium } from 'playwright';

const baseURL = (process.env.REPOARK_E2E_URL || 'https://127.0.0.1:19789').replace(/\/$/, '');
const idpURL = (process.env.REPOARK_E2E_IDP_URL || 'http://127.0.0.1:19880').replace(/\/$/, '');
const browser = await chromium.launch({ headless: true });
const pageErrors = [];
let activePage;

async function setIdentity(role) {
  const response = await fetch(`${idpURL}/__e2e/identity?role=${encodeURIComponent(role)}`, { method: 'POST' });
  assert.equal(response.status, 200, `failed to select ${role} identity`);
}

async function loginAs(role) {
  await setIdentity(role);
  const context = await browser.newContext({
    ignoreHTTPSErrors: true,
    viewport: { width: 1280, height: 900 },
  });
  const page = await context.newPage();
  activePage = page;
  page.on('pageerror', error => pageErrors.push(`${role}: ${error.message}`));

  const before = await context.request.get(`${baseURL}/api/v1/console/session`, { failOnStatusCode: false });
  assert.equal(before.status(), 401, `${role}: unauthenticated session must be rejected`);

  const home = await page.goto(baseURL, { waitUntil: 'domcontentloaded' });
  assert(home, `${role}: dashboard navigation returned no response`);
  assert.equal(home.status(), 200, `${role}: dashboard should load after OIDC redirect`);
  assert.equal((await home.allHeaders())['x-repoark-e2e-proxy'], '1', `${role}: request bypassed reverse proxy`);

  await page.locator('#session').filter({ hasText: role }).waitFor({ timeout: 10_000 });
  const sessionResponse = await context.request.get(`${baseURL}/api/v1/console/session`);
  assert.equal(sessionResponse.status(), 200);
  const session = await sessionResponse.json();
  assert.equal(session.authenticated, true);
  assert.equal(session.local, false);
  assert.equal(session.role, role);
  assert(session.csrf, `${role}: CSRF token is missing`);

  const cookies = await context.cookies(baseURL);
  const sessionCookie = cookies.find(cookie => cookie.name === 'repoark_session');
  assert(sessionCookie, `${role}: encrypted session cookie is missing`);
  assert.equal(sessionCookie.secure, true, `${role}: session cookie must stay Secure behind HTTPS proxy`);
  assert.equal(sessionCookie.sameSite, 'Strict', `${role}: session cookie must stay SameSite=Strict`);
  for (const temporary of ['repoark_oidc_state', 'repoark_oidc_nonce', 'repoark_oidc_verifier']) {
    assert(!cookies.some(cookie => cookie.name === temporary), `${role}: temporary OIDC cookie ${temporary} was not cleared`);
  }

  return { context, page, session };
}

try {
  {
    const { context, session } = await loginAs('viewer');
    const response = await context.request.post(`${baseURL}/api/v1/console/job/cancel`, {
      headers: { 'X-CSRF-Token': session.csrf },
      failOnStatusCode: false,
    });
    assert.equal(response.status(), 403, 'viewer must not mutate console state');
    await context.close();
  }

  {
    const { context, session } = await loginAs('operator');
    const withoutCSRF = await context.request.post(`${baseURL}/api/v1/console/job/cancel`, {
      failOnStatusCode: false,
    });
    assert.equal(withoutCSRF.status(), 403, 'operator mutation without CSRF must fail');
    assert.match(await withoutCSRF.text(), /CSRF token mismatch/);

    const authorized = await context.request.post(`${baseURL}/api/v1/console/job/cancel`, {
      headers: { 'X-CSRF-Token': session.csrf },
      failOnStatusCode: false,
    });
    assert.equal(authorized.status(), 409, 'operator with valid CSRF should reach the no-active-job business guard');
    await context.close();
  }

  {
    const { context, page, session } = await loginAs('admin');
    const beforeStepUp = await context.request.post(`${baseURL}/restore/approve`, {
      form: { _csrf: session.csrf, id: 'missing-e2e-approval' },
      failOnStatusCode: false,
      maxRedirects: 0,
    });
    assert.equal(beforeStepUp.status(), 403, 'admin without WebAuthn AMR must fail step-up authorization');
    assert.match(await beforeStepUp.text(), /webauthn/i);

    const stepUpHome = await page.goto(`${baseURL}/auth/step-up`, { waitUntil: 'domcontentloaded' });
    assert(stepUpHome, 'step-up navigation returned no response');
    assert.equal(stepUpHome.status(), 200, 'step-up should return to dashboard');
    const elevatedSessionResponse = await context.request.get(`${baseURL}/api/v1/console/session`);
    const elevatedSession = await elevatedSessionResponse.json();
    assert.equal(elevatedSession.role, 'admin');
    assert.notEqual(elevatedSession.csrf, session.csrf, 'step-up must rotate the encrypted session/CSRF token');

    const idpStatusResponse = await fetch(`${idpURL}/__e2e/status`);
    assert.equal(idpStatusResponse.status, 200);
    const idpStatus = await idpStatusResponse.json();
    assert(idpStatus.step_up_count >= 1, 'IdP did not observe a step-up login');
    assert.equal(idpStatus.last_acr, 'urn:repoark:e2e:webauthn');

    const afterStepUp = await context.request.post(`${baseURL}/restore/approve`, {
      form: { _csrf: elevatedSession.csrf, id: 'missing-e2e-approval' },
      failOnStatusCode: false,
      maxRedirects: 0,
    });
    assert.equal(afterStepUp.status(), 400, 'elevated admin should pass auth and fail only on the safe missing approval');
    assert.doesNotMatch(await afterStepUp.text(), /unauthorized|CSRF|step-up/i);
    await context.close();
  }

  assert.deepEqual(pageErrors, [], `browser page errors:\n${pageErrors.join('\n')}`);
  console.log('RepoArk authenticated web E2E passed: viewer/operator/admin, CSRF, HTTPS proxy, OIDC step-up');
} catch (error) {
  if (activePage && !activePage.isClosed()) {
    await activePage.screenshot({ path: `${process.env.REPOARK_E2E_ARTIFACTS || 'artifacts/web-e2e'}/auth-failure.png`, fullPage: true }).catch(() => {});
  }
  throw error;
} finally {
  await browser.close();
}
