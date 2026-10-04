import { NavLink, Outlet, useNavigate } from 'react-router-dom';
import { logout } from '../api';

export default function Layout() {
  const navigate = useNavigate();

  const onLogout = async () => {
    try {
      await logout();
    } catch {
      // even if the server call fails, drop to the login page
    }
    navigate('/login');
  };

  return (
    <div className="app">
      <aside className="sidebar">
        <div className="brand">
          atomic<span className="accent">-updater</span>
        </div>
        <nav>
          <NavLink to="/" end className={({ isActive }) => (isActive ? 'active' : '')}>
            Fleet
          </NavLink>
          <NavLink to="/devices" className={({ isActive }) => (isActive ? 'active' : '')}>
            Devices
          </NavLink>
          <NavLink to="/bundles" className={({ isActive }) => (isActive ? 'active' : '')}>
            Bundles
          </NavLink>
          <NavLink to="/rollouts" className={({ isActive }) => (isActive ? 'active' : '')}>
            Rollouts
          </NavLink>
        </nav>
        <button onClick={() => void onLogout()}>Log out</button>
      </aside>
      <main className="content">
        <Outlet />
      </main>
    </div>
  );
}
