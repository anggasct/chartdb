import React, { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button } from '@/components/button/button';
import { api, ApiClientError } from '@/api-client/client';
import type { OIDCProviderInfo } from '@/api-client/client';
import { useAuth } from '@/hooks/use-auth';

export const LoginPage: React.FC = () => {
    const navigate = useNavigate();
    const { account, loading, login, register } = useAuth();
    const [mode, setMode] = useState<'login' | 'register'>('login');
    const [email, setEmail] = useState('');
    const [password, setPassword] = useState('');
    const [displayName, setDisplayName] = useState('');
    const [providers, setProviders] = useState<OIDCProviderInfo[]>([]);
    const [error, setError] = useState<string | null>(null);
    const [busy, setBusy] = useState(false);

    useEffect(() => {
        api.oidcProviders()
            .then((result) => {
                setProviders(result.providers);
            })
            .catch(() => {
                setProviders([]);
            });
    }, []);

    useEffect(() => {
        if (!loading && account) {
            navigate('/diagrams', { replace: true });
        }
    }, [loading, account, navigate]);

    const submit = async (event: React.FormEvent) => {
        event.preventDefault();
        setBusy(true);
        setError(null);
        try {
            if (mode === 'login') {
                await login({ email, password });
            } else {
                await register({ email, password, displayName });
            }
            navigate('/diagrams', { replace: true });
        } catch (err) {
            setError(
                err instanceof ApiClientError
                    ? err.message
                    : 'Something went wrong.'
            );
        } finally {
            setBusy(false);
        }
    };

    if (loading) {
        return <p className="p-8 text-center text-slate-500">Loading…</p>;
    }

    return (
        <section className="bg-white">
            <div className="mx-auto max-w-screen-sm px-4 py-8 lg:py-16">
                <h1 className="mb-4 text-3xl font-bold tracking-tight">
                    Log in to ChartDB
                </h1>
                <form onSubmit={submit} className="space-y-4">
                    <label className="block text-sm font-medium">
                        Email
                        <input
                            type="email"
                            required
                            value={email}
                            onChange={(event) => setEmail(event.target.value)}
                            className="mt-1 block w-full rounded-md border border-solid border-slate-300 px-3 py-2"
                        />
                    </label>
                    <label className="block text-sm font-medium">
                        Password
                        <input
                            type="password"
                            required
                            minLength={8}
                            value={password}
                            onChange={(event) =>
                                setPassword(event.target.value)
                            }
                            className="mt-1 block w-full rounded-md border border-solid border-slate-300 px-3 py-2"
                        />
                    </label>
                    {mode === 'register' ? (
                        <label className="block text-sm font-medium">
                            Display name (optional)
                            <input
                                type="text"
                                value={displayName}
                                onChange={(event) =>
                                    setDisplayName(event.target.value)
                                }
                                className="mt-1 block w-full rounded-md border border-solid border-slate-300 px-3 py-2"
                            />
                        </label>
                    ) : null}
                    {error ? (
                        <p className="text-sm text-red-600">{error}</p>
                    ) : null}
                    <Button type="submit" disabled={busy} className="w-full">
                        {mode === 'login' ? 'Log in' : 'Create account'}
                    </Button>
                </form>
                <p className="mt-4 text-center text-sm">
                    {mode === 'login' ? (
                        <>
                            No account yet?{' '}
                            <button
                                type="button"
                                className="underline"
                                onClick={() => setMode('register')}
                            >
                                Register
                            </button>
                        </>
                    ) : (
                        <>
                            Already have an account?{' '}
                            <button
                                type="button"
                                className="underline"
                                onClick={() => setMode('login')}
                            >
                                Log in
                            </button>
                        </>
                    )}
                </p>
                {providers.length > 0 ? (
                    <div className="mt-6 border-t border-solid border-slate-200 pt-4">
                        <p className="mb-2 text-center text-sm text-slate-500">
                            Or continue with
                        </p>
                        <div className="space-y-2">
                            {providers.map((provider) => (
                                <Button
                                    key={provider.id}
                                    variant="outline"
                                    className="w-full"
                                    onClick={() => {
                                        window.location.href = `/api/v1/auth/login/oidc?provider=${encodeURIComponent(provider.id)}`;
                                    }}
                                >
                                    {provider.name}
                                </Button>
                            ))}
                        </div>
                    </div>
                ) : null}
            </div>
        </section>
    );
};
