import React from 'react';
import { describe, expect, it, vi, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { SharedViewPage } from './shared-view-page';

function json(body: unknown, status = 200): Response {
    return new Response(JSON.stringify(body), { status });
}

function renderAt(token: string) {
    render(
        <MemoryRouter initialEntries={[`/s/${token}`]}>
            <Routes>
                <Route path="/s/:token" element={<SharedViewPage />} />
            </Routes>
        </MemoryRouter>
    );
}

describe('SharedViewPage', () => {
    afterEach(() => {
        vi.unstubAllGlobals();
    });
    it('renders a valid share read-only with zero editor mounts', async () => {
        vi.stubGlobal('fetch', async (url: string) => {
            if (url === '/api/v1/s/good-token') {
                return json({
                    title: 'Shared Shop',
                    document: { tables: [] },
                    version: 3,
                });
            }
            return new Response('null', { status: 500 });
        });
        const { container } = render(
            <MemoryRouter initialEntries={[`/s/good-token`]}>
                <Routes>
                    <Route path="/s/:token" element={<SharedViewPage />} />
                </Routes>
            </MemoryRouter>
        );
        expect(await screen.findByText('Shared Shop')).toBeTruthy();
        expect(screen.getByText(/read-only shared view/i)).toBeTruthy();
        expect(container.querySelector('canvas')).toBeNull();
        expect(container.querySelector('[data-testid="canvas"]')).toBeNull();
    });

    it('shows the invalid state for unknown tokens', async () => {
        vi.stubGlobal('fetch', async () =>
            json({ error: { code: 'not_found', message: 'x' } }, 404)
        );
        renderAt('bad-token');
        expect(await screen.findByText('Link unavailable')).toBeTruthy();
    });
});
