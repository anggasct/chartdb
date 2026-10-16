import { createContext } from 'react';
import type { Account, Identity } from '@/api-client/client';

export interface AuthContext {
    account: Account | null;
    identities: Identity[];
    loading: boolean;
    login: (params: { email: string; password: string }) => Promise<void>;
    register: (params: {
        email: string;
        password: string;
        displayName?: string;
    }) => Promise<void>;
    logout: () => Promise<void>;
    refresh: () => Promise<void>;
}

export const AuthContext = createContext<AuthContext>({
    account: null,
    identities: [],
    loading: true,
    login: () => Promise.reject(new Error('AuthProvider is missing.')),
    register: () => Promise.reject(new Error('AuthProvider is missing.')),
    logout: () => Promise.reject(new Error('AuthProvider is missing.')),
    refresh: () => Promise.reject(new Error('AuthProvider is missing.')),
});
