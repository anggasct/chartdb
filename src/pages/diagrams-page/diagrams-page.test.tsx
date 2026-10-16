import React from 'react';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { AuthProvider } from '@/auth/auth-provider';
import { DiagramsPage } from './diagrams-page';

function json(body: unknown, status = 200): Response {
    return new Response(JSON.stringify(body), { status });
}

const account = {
    id: 'a1',
    display_name: 'Member',
    primary_email: 'm@example.test',
};

function stubList(diagrams: unknown[]) {
    vi.stubGlobal('fetch', async (url: string) => {
        if (url === '/api/v1/me') {
            return json({ account, identities: [] });
        }
        if (url.startsWith('/api/v1/diagram')) {
            return json({ diagrams, next_cursor: '' });
        }
        return new Response('null', { status: 500 });
    });
}

function renderList() {
    render(
        <MemoryRouter initialEntries={['/diagrams']}>
            <AuthProvider>
                <Routes>
                    <Route path="/diagrams" element={<DiagramsPage />} />
                </Routes>
            </AuthProvider>
        </MemoryRouter>
    );
}

describe('DiagramsPage', () => {
    beforeEach(() => {
        vi.unstubAllGlobals();
    });

    it('renders the diagram list at /diagrams', async () => {
        stubList([
            {
                id: 'd1',
                title: 'Shop',
                head_version: 2,
                created_at: '2026-10-15T00:00:00Z',
                updated_at: '2026-10-15T00:00:00Z',
            },
        ]);
        renderList();
        expect(
            await screen.findByRole('heading', { name: 'My diagrams' })
        ).toBeTruthy();
        expect(await screen.findByText('Shop')).toBeTruthy();
    });

    it('shows the empty state without diagrams', async () => {
        stubList([]);
        renderList();
        expect(await screen.findByText(/no diagrams yet/i)).toBeTruthy();
    });
});
