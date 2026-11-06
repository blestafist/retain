import { useState, type FormEvent } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ApiError, login, register, type LoginRequest, type RegisterRequest } from '../api/client';
import { t, type TranslationKey } from '../i18n/en';
import { Button } from '../ui/Button';
import { meQueryKey } from './session';

type AuthMode = 'signin' | 'signup';
type FieldName = 'invite' | 'email' | 'name' | 'password';
type FieldErrors = Partial<Record<FieldName, TranslationKey>>;

function hasValidEmail(email: string) {
  return /^[^\s@]+@[^\s@.]+\.[^\s@]+$/.test(email);
}

function errorMessage(error: unknown): string {
  if (error instanceof ApiError && error.serverMessage) return error.serverMessage;
  return t('requestFailed');
}

function ErrorNotice({ error }: { error: unknown }) {
  if (!error) return null;
  return <p className="auth-form-error" role="alert">{errorMessage(error)}</p>;
}

function FieldError({ id, error }: { id: string; error?: TranslationKey }) {
  if (!error) return null;
  return <span className="auth-field-error" id={`${id}-error`}>{t(error)}</span>;
}

function fieldDescription(error: TranslationKey | undefined, hintId: string) {
  return error ? `${hintId} ${hintId.replace(/-hint$/, '-error')}` : hintId;
}

function focusFirstInvalidField(prefix: string, order: readonly FieldName[], errors: FieldErrors) {
  const field = order.find((name) => errors[name]);
  if (field) document.getElementById(`${prefix}-${field}`)?.focus();
}

function SignInForm() {
  const queryClient = useQueryClient();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const mutation = useMutation({
    mutationFn: (input: LoginRequest) => login(input),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: meQueryKey }),
  });

  function clearFieldError(field: FieldName) {
    setFieldErrors((current) => ({ ...current, [field]: undefined }));
    mutation.reset();
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const errors: FieldErrors = {};
    if (!email.trim()) errors.email = 'emailRequired';
    else if (!hasValidEmail(email.trim())) errors.email = 'emailInvalid';
    if (!password) errors.password = 'passwordRequired';
    setFieldErrors(errors);
    mutation.reset();
    if (Object.keys(errors).length > 0) {
      focusFirstInvalidField('signin', ['email', 'password'], errors);
      return;
    }
    mutation.mutate({ email: email.trim(), password });
  }

  return (
    <>
      <p className="auth-kicker">{t('accountAccess')}</p>
      <h1>{t('signInHeading')}</h1>
      <p className="auth-description">{t('signInDescription')}</p>
      <form className="auth-form" noValidate aria-busy={mutation.isPending} onSubmit={submit}>
        <ErrorNotice error={mutation.error} />
        <div className="auth-field">
          <label htmlFor="signin-email">{t('email')}</label>
          <input
            autoComplete="email"
            className="auth-input"
            id="signin-email"
            name="email"
            type="email"
            required
            disabled={mutation.isPending}
            aria-invalid={fieldErrors.email ? true : undefined}
            aria-describedby={fieldDescription(fieldErrors.email, 'signin-email-hint')}
            value={email}
            onChange={(event) => { setEmail(event.target.value); clearFieldError('email'); }}
          />
          <span className="auth-field-hint" id="signin-email-hint">{t('emailHint')}</span>
          <FieldError id="signin-email" error={fieldErrors.email} />
        </div>
        <div className="auth-field">
          <label htmlFor="signin-password">{t('password')}</label>
          <input
            autoComplete="current-password"
            className="auth-input"
            id="signin-password"
            name="password"
            type="password"
            required
            disabled={mutation.isPending}
            aria-invalid={fieldErrors.password ? true : undefined}
            aria-describedby={fieldErrors.password ? 'signin-password-error' : undefined}
            value={password}
            onChange={(event) => { setPassword(event.target.value); clearFieldError('password'); }}
          />
          <FieldError id="signin-password" error={fieldErrors.password} />
        </div>
        <Button className="auth-submit" type="submit" disabled={mutation.isPending}>
          {t(mutation.isPending ? 'signingIn' : 'signIn')}
        </Button>
      </form>
      <p className="auth-switch">{t('needInvite')} <a href="/signup">{t('createAccount')}</a></p>
    </>
  );
}

function SignUpForm() {
  const [invite, setInvite] = useState(() => new URLSearchParams(window.location.search).get('invite') ?? '');
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const mutation = useMutation({ mutationFn: (input: RegisterRequest) => register(input) });

  function clearFieldError(field: FieldName) {
    setFieldErrors((current) => ({ ...current, [field]: undefined }));
    mutation.reset();
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const errors: FieldErrors = {};
    if (!invite.trim()) errors.invite = 'inviteRequired';
    if (!email.trim()) errors.email = 'emailRequired';
    else if (!hasValidEmail(email.trim())) errors.email = 'emailInvalid';
    if (!name.trim()) errors.name = 'nameRequired';
    if (!password) errors.password = 'passwordRequired';
    else if (password.length < 12) errors.password = 'passwordTooShort';
    setFieldErrors(errors);
    mutation.reset();
    if (Object.keys(errors).length > 0) {
      focusFirstInvalidField('signup', ['invite', 'name', 'email', 'password'], errors);
      return;
    }
    mutation.mutate({ invite: invite.trim(), email: email.trim(), name: name.trim(), password });
  }

  if (mutation.isSuccess) {
    return (
      <section className="auth-success" aria-live="polite">
        <p className="auth-kicker">{t('accountCreated')}</p>
        <h1>{t('accountReadyHeading')}</h1>
        <p className="auth-description">{t('accountReadyPrompt')}</p>
        <a className="auth-primary-link" href="/">{t('signIn')}</a>
      </section>
    );
  }

  return (
    <>
      <p className="auth-kicker">{t('inviteOnly')}</p>
      <h1>{t('signUpHeading')}</h1>
      <p className="auth-description">{t('signUpDescription')}</p>
      <form className="auth-form" noValidate aria-busy={mutation.isPending} onSubmit={submit}>
        <ErrorNotice error={mutation.error} />
        <div className="auth-field">
          <label htmlFor="signup-invite">{t('invitationCode')}</label>
          <input
            autoComplete="off"
            className="auth-input"
            id="signup-invite"
            name="invite"
            type="text"
            required
            disabled={mutation.isPending}
            aria-invalid={fieldErrors.invite ? true : undefined}
            aria-describedby={fieldDescription(fieldErrors.invite, 'signup-invite-hint')}
            value={invite}
            onChange={(event) => { setInvite(event.target.value); clearFieldError('invite'); }}
          />
          <span className="auth-field-hint" id="signup-invite-hint">{t('invitationCodeHint')}</span>
          <FieldError id="signup-invite" error={fieldErrors.invite} />
        </div>
        <div className="auth-field">
          <label htmlFor="signup-name">{t('name')}</label>
          <input
            autoComplete="name"
            className="auth-input"
            id="signup-name"
            name="name"
            type="text"
            required
            disabled={mutation.isPending}
            aria-invalid={fieldErrors.name ? true : undefined}
            aria-describedby={fieldErrors.name ? 'signup-name-error' : undefined}
            value={name}
            onChange={(event) => { setName(event.target.value); clearFieldError('name'); }}
          />
          <FieldError id="signup-name" error={fieldErrors.name} />
        </div>
        <div className="auth-field">
          <label htmlFor="signup-email">{t('email')}</label>
          <input
            autoComplete="email"
            className="auth-input"
            id="signup-email"
            name="email"
            type="email"
            required
            disabled={mutation.isPending}
            aria-invalid={fieldErrors.email ? true : undefined}
            aria-describedby={fieldDescription(fieldErrors.email, 'signup-email-hint')}
            value={email}
            onChange={(event) => { setEmail(event.target.value); clearFieldError('email'); }}
          />
          <span className="auth-field-hint" id="signup-email-hint">{t('emailHint')}</span>
          <FieldError id="signup-email" error={fieldErrors.email} />
        </div>
        <div className="auth-field">
          <label htmlFor="signup-password">{t('password')}</label>
          <input
            autoComplete="new-password"
            className="auth-input"
            id="signup-password"
            name="password"
            type="password"
            minLength={12}
            required
            disabled={mutation.isPending}
            aria-invalid={fieldErrors.password ? true : undefined}
            aria-describedby={fieldDescription(fieldErrors.password, 'signup-password-hint')}
            value={password}
            onChange={(event) => { setPassword(event.target.value); clearFieldError('password'); }}
          />
          <span className="auth-field-hint" id="signup-password-hint">{t('passwordRequirement')}</span>
          <FieldError id="signup-password" error={fieldErrors.password} />
        </div>
        <Button className="auth-submit" type="submit" disabled={mutation.isPending}>
          {t(mutation.isPending ? 'creatingAccount' : 'createAccount')}
        </Button>
      </form>
      <p className="auth-switch">{t('alreadyRegistered')} <a href="/">{t('signIn')}</a></p>
    </>
  );
}

export function AuthForms({ mode }: { mode: AuthMode }) {
  return <section className="auth-card">{mode === 'signup' ? <SignUpForm /> : <SignInForm />}</section>;
}
