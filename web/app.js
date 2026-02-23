const card = document.querySelector('.card');
const nameEl = document.querySelector('[data-name]');
const emailEl = document.querySelector('[data-email]');
const avatarEl = document.querySelector('[data-avatar]');
const errorEl = document.querySelector('[data-error]');
const form = document.querySelector('.form');
const passwordInput = form.querySelector('input[name="password"]');
const actionBtn = document.querySelector('[data-action]');

const state = {
  exists: false,
  goto: null,
  completed: false,
};

document.addEventListener('DOMContentLoaded', () => {
  loadProfile();
});

form.addEventListener('submit', (event) => {
  event.preventDefault();
  if (state.completed && state.goto) {
    window.location.href = state.goto;
    return;
  }
  submitPassword(passwordInput.value);
});

actionBtn.addEventListener('click', (event) => {
  if (state.completed && state.goto) {
    event.preventDefault();
    window.location.href = state.goto;
  }
});

async function loadProfile() {
  setLoading(true);
  hideError();
  try {
    const res = await fetch('api/me', { credentials: 'include' });
    if (!res.ok) throw new Error('Failed to load profile');
    const data = await res.json();
    state.exists = Boolean(data.exists);
    state.goto = data.nextUrl;
    renderUser(data);
    renderAction();
  } catch (err) {
    showError('Что-то пошло не так. Попробуйте позже.');
  } finally {
    setLoading(false);
  }
}

async function submitPassword(password) {
  if (!password) {
    showError('Password is required.');
    return;
  }
  setLoading(true);
  hideError();
  try {
    const res = await fetch('api/password', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ password }),
    });
    const data = await res.json();
    if (!res.ok) {
      const message = data?.error?.message || 'Не удалось выполнить операцию. Обратитесь к администратору.';
      throw new Error(message);
    }
    state.completed = true;
    state.goto = data.nextUrl;
    renderSuccess(data.action === 'updated');
  } catch (err) {
    showError(err.message);
  } finally {
    setLoading(false);
  }
}

function renderUser(data) {
  const name = data.name?.trim() || 'User';
  nameEl.textContent = `Hi, ${name}`;
  emailEl.textContent = data.email;
  if (data.picture) {
    avatarEl.style.backgroundImage = `url(${data.picture})`;
    avatarEl.textContent = '';
  } else {
    avatarEl.style.backgroundImage = 'none';
    avatarEl.textContent = name.charAt(0).toUpperCase();
  }
}

function renderAction() {
  state.completed = false;
  actionBtn.classList.remove('success');
  actionBtn.textContent = state.exists ? 'Update password' : 'Create Navidrome account';
}

function renderSuccess(updated) {
  actionBtn.classList.add('success');
  actionBtn.textContent = 'Log in Navidrome';
  passwordInput.value = '';
  if (!updated) {
    state.exists = true;
  }
}

function setLoading(isLoading) {
  if (isLoading) {
    actionBtn.setAttribute('disabled', 'true');
    card.setAttribute('data-state', 'loading');
  } else if (!state.completed) {
    actionBtn.removeAttribute('disabled');
    card.setAttribute('data-state', 'ready');
  } else {
    actionBtn.removeAttribute('disabled');
  }
}

function showError(message) {
  if (!message) return;
  errorEl.textContent = message;
  errorEl.hidden = false;
}

function hideError() {
  errorEl.hidden = true;
  errorEl.textContent = '';
}
