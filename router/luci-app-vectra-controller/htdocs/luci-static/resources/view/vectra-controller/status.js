'use strict';
'require view';
'require form';
'require fs';
'require ui';

function textOrFallback(value, fallback) {
	if (value == null)
		return fallback;

	value = String(value).trim();
	return value !== '' ? value : fallback;
}

function parseStatus(stdout) {
	if (!stdout)
		return {};

	try {
		return JSON.parse(stdout);
	}
	catch (err) {
		return {
			parseError: err.message || String(err)
		};
	}
}

function renderStatusRow(label, value) {
	return E('tr', {}, [
		E('td', { 'class': 'td left', 'style': 'width:35%' }, [ label ]),
		E('td', { 'class': 'td left' }, [ value ])
	]);
}

function renderBadge(value, kind) {
	return E('span', {
		'class': 'label',
		'style': 'display:inline-block;padding:0.2rem 0.55rem;border-radius:999px;font-weight:600;background:' +
			(kind === 'ok' ? '#dcfce7;color:#166534' : '#fef3c7;color:#92400e')
	}, [ value ]);
}

function boolBadge(value, okLabel, warnLabel) {
	return value ? renderBadge(okLabel, 'ok') : renderBadge(warnLabel, 'warn');
}

function serviceBadge(value) {
	return value === 'running'
		? renderBadge(_('работает'), 'ok')
		: renderBadge(_('остановлен'), 'warn');
}

function passwallBadge(status) {
	if (status.passwallEnabled === '1')
		return renderBadge(_('включен'), 'ok');

	return renderBadge(_('выключен'), 'warn');
}

function isDirectLike(status) {
	return String(status.rescueMode || '') === 'direct' || status.passwallEnabled !== '1';
}

function importStateLabel(value) {
	switch (String(value || '')) {
	case 'approved':
		return renderBadge(_('одобрен'), 'ok');
	case 'import_review':
		return renderBadge(_('ожидает проверки импорта'), 'warn');
	case 'out_of_sync':
		return renderBadge(_('требует повторного импорта'), 'warn');
	case 'pending':
		return renderBadge(_('ожидает синхронизации'), 'warn');
	default:
		return textOrFallback(value, _('неизвестно'));
	}
}

function rescueModeLabel(value) {
	switch (String(value || '')) {
	case 'direct':
		return renderBadge(_('прямой режим'), 'warn');
	case 'proxy':
		return renderBadge(_('прокси-режим'), 'ok');
	default:
		return textOrFallback(value, _('неизвестно'));
	}
}

function reachabilityBadge(value) {
	return value === true
		? renderBadge(_('доступен'), 'ok')
		: renderBadge(_('недоступен'), 'warn');
}

function publicProbeBadge(status) {
	if (status.publicReachable === true)
		return renderBadge(_('доступны'), 'ok');

	if (!isDirectLike(status))
		return renderBadge(_('не подтверждены'), 'warn');

	return renderBadge(_('недоступны'), 'warn');
}

function formatSelectedNode(status) {
	var label = textOrFallback(status.selectedNodeLabel, '');
	var nodeId = textOrFallback(status.selectedNodeId, '');

	if (label && nodeId && label !== nodeId)
		return '%s (%s)'.format(label, nodeId);

	return textOrFallback(label || nodeId, _('не выбрана'));
}

function findRawDegradedMessage(status) {
	var candidates = [
		status.lastRescueReason,
		status.lastOperatorMessage,
		status.lastError
	];

	for (var i = 0; i < candidates.length; i++) {
		var candidate = textOrFallback(candidates[i], '');
		if (candidate === 'Subscription expired or upstream proxy unavailable')
			return candidate;
	}

	return '';
}

function renderInfoAlert(title, message, kind) {
	return E('div', {
		'class': 'alert-message ' + (kind || 'notice'),
		'style': 'margin-top:1rem'
	}, [
		E('strong', {}, [ title ]),
		E('div', { 'style': 'margin-top:0.35rem' }, [ message ])
	]);
}

return view.extend({
	load: function() {
		return fs.exec('/usr/libexec/vectra-controller/luci-bridge.sh', [ 'status' ])
			.then(function(res) {
				return parseStatus(res.stdout);
			})
			.catch(function(err) {
				return {
					execError: err.message || String(err)
				};
			});
	},

	handleBridgeAction: function(action, message) {
		return fs.exec('/usr/libexec/vectra-controller/luci-bridge.sh', [ action ])
			.then(function() {
				ui.addNotification(null, E('p', message), 'info');
				window.setTimeout(function() { window.location.reload(); }, 400);
			})
			.catch(function(err) {
				ui.addNotification(null, E('p', err.message || String(err)), 'danger');
			});
	},

	render: function(status) {
		var rawDegradedMessage = findRawDegradedMessage(status);
		var directLike = isDirectLike(status);
		var degradedActive = directLike && !!rawDegradedMessage;
		var canClearRescue = directLike ||
			!!textOrFallback(status.lastRescueReason, '') ||
			!!textOrFallback(status.lastRescueAt, '');
		var map = new form.Map('vectra-controller', _('Контроллер Vectra'),
			_('Локальная консоль для первичного подключения, диагностики и аварийных действий без возврата к старому Lua-интерфейсу.'));
		var section = map.section(form.NamedSection, 'main', 'controller', _('Параметры подключения'));
		var option;

		section.anonymous = true;
		section.addremove = false;

		option = section.option(form.Value, 'control_url', _('URL управляющего API'));
		option.placeholder = 'https://api.vectra-pro.net';
		option.rmempty = false;

		option = section.option(form.Value, 'panel_url', _('URL панели'));
		option.placeholder = 'https://router.vectra-pro.net';
		option.rmempty = false;

		option = section.option(form.Value, 'poll_interval', _('Интервал опроса'));
		option.placeholder = '45s';

		option = section.option(form.Value, 'request_timeout', _('Таймаут запроса'));
		option.placeholder = '10s';

		var statusTable = E('div', { 'class': 'cbi-section' }, [
			E('h3', {}, [ _('Локальный статус') ]),
			E('table', { 'class': 'table' }, [
				renderStatusRow(_('Сервис контроллера Vectra'),
					serviceBadge(status.serviceState)),
				renderStatusRow(_('Сервис PassWall2'),
					serviceBadge(status.passwallServiceState)),
				renderStatusRow(_('Главный переключатель PassWall2'),
					passwallBadge(status)),
				renderStatusRow(_('URL управляющего API'),
					textOrFallback(status.controlUrl, _('не настроен'))),
				renderStatusRow(_('URL панели'),
					textOrFallback(status.panelUrl, _('совпадает с Control URL'))),
				renderStatusRow(_('Версия пакета контроллера'),
					textOrFallback(status.controllerVersion, _('не установлена'))),
				renderStatusRow(_('Версия пакета LuCI'),
					textOrFallback(status.luciVersion, _('не установлена'))),
				renderStatusRow(_('Идентификатор роутера'),
					textOrFallback(status.routerId, _('еще не зарегистрирован'))),
				renderStatusRow(_('Статус одобрения'),
					boolBadge(status.pendingApproval === true, _('ожидает проверки'), _('одобрен или не запрошен'))),
				renderStatusRow(_('Статус импорта'),
					importStateLabel(status.importState)),
				renderStatusRow(_('Режим rescue'),
					rescueModeLabel(directLike ? 'direct' : status.rescueMode)),
				renderStatusRow(_('Доступность управляющего API'),
					reachabilityBadge(status.serverReachable === true)),
				renderStatusRow(_('Публичные контрольные URL'),
					publicProbeBadge(status)),
				renderStatusRow(_('Выбранная нода'),
					formatSelectedNode(status)),
				renderStatusRow(_('Счетчик сбоев прокси'),
					textOrFallback(status.proxyFailureCount, '0')),
				renderStatusRow(_('Счетчик успешных проверок восстановления'),
					textOrFallback(status.proxySuccessCount, '0')),
				renderStatusRow(_('Счетчик успешных проверок прямого режима'),
					textOrFallback(status.directSuccessCount, '0')),
				renderStatusRow(_('Ожидающие задания'),
					textOrFallback(status.jobsAvailable, '0')),
				renderStatusRow(_('Последняя примененная ревизия'),
					textOrFallback(status.appliedRevisionId, _('ревизия еще не применялась'))),
				renderStatusRow(_('Отпечаток конфигурации'),
					textOrFallback(status.configDigest, _('еще не записан'))),
				renderStatusRow(_('Последняя регистрация'),
					textOrFallback(status.lastRegisterAt, _('регистрация еще не выполнялась'))),
				renderStatusRow(_('Последнее сообщение оператора'),
					textOrFallback(status.lastOperatorMessage, _('сообщений еще не было'))),
				renderStatusRow(_('Последняя причина rescue'),
					textOrFallback(status.lastRescueReason, _('аварийных событий еще не было'))),
				renderStatusRow(_('Последнее время rescue'),
					textOrFallback(status.lastRescueAt, _('нет записанного события'))),
				renderStatusRow(_('Последняя ошибка управляющего API'),
					textOrFallback(status.lastServerError, _('ошибок не было'))),
				renderStatusRow(_('Последняя ошибка контрольного URL'),
					textOrFallback(status.lastPublicError, _('ошибок не было'))),
				renderStatusRow(_('Последнее локальное обновление статуса'),
					textOrFallback(status.lastCheckInAt, _('локальный статус еще не записан')))
			])
		]);

		if (status.parseError || status.execError)
			statusTable.appendChild(renderInfoAlert(
				_('Не удалось прочитать локальный статус'),
				_('Мост LuCI вернул ошибку: %s').format(status.parseError || status.execError),
				'warning'
			));

		if (status.lastError)
			statusTable.appendChild(renderInfoAlert(
				_('Последняя ошибка агента'),
				status.lastError,
				'warning'
			));

		if (degradedActive)
			statusTable.appendChild(renderInfoAlert(
				_('Обнаружено аварийное состояние'),
				_('Исходное сообщение: %s. По-русски: подписка истекла, недоступен upstream-прокси или текущая прокси-схема перестала работать и роутер перешел в аварийный прямой режим.')
					.format(rawDegradedMessage),
				'warning'
			));

		if (!degradedActive && !directLike && status.publicReachable !== true && textOrFallback(status.lastPublicError, '') !== '')
			statusTable.appendChild(renderInfoAlert(
				_('Контрольный URL не подтвердился'),
				_('Локальная проверка одного из контрольных URL завершилась ошибкой, но это само по себе не означает, что интернет недоступен или что роутер должен уходить в прямой режим. Последняя ошибка: %s')
					.format(status.lastPublicError),
				'notice'
			));

		var actionItems = [
			E('button', {
				'class': 'cbi-button cbi-button-action',
				'click': ui.createHandlerFn(this, function() {
					return this.handleBridgeAction('render', _('Рабочая конфигурация пересобрана.'));
				})
			}, [ _('Пересобрать рабочую конфигурацию') ]),
			E('button', {
				'class': 'cbi-button cbi-button-apply',
				'click': ui.createHandlerFn(this, function() {
					return this.handleBridgeAction('reconnect', _('Переподключение агента запрошено.'));
				})
			}, [ _('Переподключить агент') ]),
			E('button', {
				'class': 'cbi-button cbi-button-negative important',
				'click': ui.createHandlerFn(this, function() {
					return this.handleBridgeAction('direct', _('PassWall2 выключен, роутер переведен в прямой режим.'));
				})
			}, [ _('Аварийный прямой режим') ])
		];

		if (canClearRescue)
			actionItems.push(E('button', {
				'class': 'cbi-button cbi-button-positive',
				'click': ui.createHandlerFn(this, function() {
					return this.handleBridgeAction(
						'resume',
						directLike
							? _('Аварийный режим отключен, PassWall2 снова включен.')
							: _('Флаг rescue очищен, роутер заново подтверждает прокси-режим.')
					);
				})
			}, [
				directLike
					? _('Отключить аварийный режим')
					: _('Сбросить rescue-флаг')
			]));

		actionItems.push(E('button', {
			'class': 'cbi-button',
			'click': ui.createHandlerFn(this, function() {
				window.location.reload();
			})
		}, [ _('Обновить статус') ]));

		var actions = E('div', {
			'class': 'cbi-section',
			'style': 'display:flex;gap:0.75rem;flex-wrap:wrap'
		}, actionItems);

		return map.render().then(function(mapEl) {
			return E([], [
				statusTable,
				actions,
				mapEl
			]);
		});
	}
});
