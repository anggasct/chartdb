import React from 'react';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { AuthProvider } from '@/auth/auth-provider';
import { LoginPage } from './login-page';
import { DiagramsPage } from '../diagrams-page/diagrams-page';

type Handler = (url: string, init?: RequestInit) => Response;

let handler: Handler;

beforeEach(() => {
    handler = () => new Response('null', { status: 500 });
    vi.stubGlobal('fetch', async (url: string, init?: RequestInit) =>
        handler(url, init)
    );
});

function json(body: unknown, status = 200): Response {
    return new Response(JSON.stringify(body), { status });
}

const unauthenticated = () => {
    handler = (url: string) => {
        if (url === '/api/v1/me') {
            return json({ error: { code: 'unauthorized', message: 'x' } }, 401);
        }
        return new Response('null', { status: 500 });
    };
};

describe('LoginPage', () => {
    it('renders the local form at /login', async () => {
        unauthenticated();
        render(
            <MemoryRouter initialEntries={['/login']}>
                <AuthProvider>
                    <LoginPage />
                </AuthProvider>
            </MemoryRouter>
        );
        expect(
            await screen.findByRole('heading', { name: 'Log in to ChartDB' })
        ).toBeTruthy();
        expect(screen.getByLabelText(/email/i)).toBeTruthy();
        expect(screen.getByLabelText(/password/i)).toBeTruthy();
        expect(screen.getByRole('button', { name: 'Log in' })).toBeTruthy();
    });

    it('logs in and lands on /diagrams', async () => {
        let loggedIn = false;
        const me = {
            account: {
                id: 'a1',
                display_name: 'Member',
                primary_email: 'm@example.test',
            },
            identities: [],
        };
        handler = (url: string) => {
            if (url === '/api/v1/me') {
                return loggedIn
                    ? json(me)
                    : json(
                          { error: { code: 'unauthorized', message: 'x' } },
                          401
                      );
            }
            if (url === '/api/v1/auth/login') {
                loggedIn = true;
                return json(me.account);
            }
            if (url.startsWith('/api/v1/diagram')) {
                return json({ diagrams: [], next_cursor: '' });
            }
            return new Response('null', { status: 500 });
        };
        render(
            <MemoryRouter initialEntries={['/login']}>
                <AuthProvider>
                    <Routes>
                        <Route path="/login" element={<LoginPage />} />
                        <Route path="/diagrams" element={<DiagramsPage />} />
                    </Routes>
                </AuthProvider>
            </MemoryRouter>
        );
        fireEvent.change(await screen.findByLabelText(/email/i), {
            target: { value: 'm@example.test' },
        });
        fireEvent.change(screen.getByLabelText(/password/i), {
            target: { value: 'password-123' },
        });
        fireEvent.click(screen.getByRole('button', { name: 'Log in' }));
        expect(
            await screen.findByRole('heading', { name: 'My diagrams' })
        ).toBeTruthy();
    });
});
