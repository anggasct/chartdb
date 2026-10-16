import React, { useCallback, useEffect, useState } from 'react';
import { AuthContext } from './auth-context';
import { api, ApiClientError } from '@/api-client/client';
import type { Account, Identity } from '@/api-client/client';

export const AuthProvider: React.FC<React.PropsWithChildren> = ({
    children,
}) => {
    const [account, setAccount] = useState<Account | null>(null);
    const [identities, setIdentities] = useState<Identity[]>([]);
    const [loading, setLoading] = useState(true);

    const refresh = useCallback(async () => {
        try {
            const me = await api.me();
            setAccount(me.account);
            setIdentities(me.identities);
        } catch (error) {
            if (error instanceof ApiClientError && error.status === 401) {
                setAccount(null);
                setIdentities([]);
                return;
            }
            throw error;
        }
    }, []);

    useEffect(() => {
        refresh()
            .catch(() => {
                setAccount(null);
                setIdentities([]);
            })
            .finally(() => {
                setLoading(false);
            });
    }, [refresh]);

    const login = useCallback(
        async (params: { email: string; password: string }) => {
            const next = await api.login(params.email, params.password);
            setAccount(next);
            await refresh();
        },
        [refresh]
    );

    const register = useCallback(
        async (params: {
            email: string;
            password: string;
            displayName?: string;
        }) => {
            const next = await api.register(
                params.email,
                params.password,
                params.displayName
            );
            setAccount(next);
            await refresh();
        },
        [refresh]
    );

    const logout = useCallback(async () => {
        await api.logout().catch(() => undefined);
        setAccount(null);
        setIdentities([]);
    }, []);

    return (
        <AuthContext.Provider
            value={{
                account,
                identities,
                loading,
                login,
                register,
                logout,
                refresh,
            }}
        >
            {children}
        </AuthContext.Provider>
    );
};
