/****************************************************************************
** Meta object code from reading C++ file 'farmapiclient.h'
**
** Created by: The Qt Meta Object Compiler version 69 (Qt 6.11.2)
**
** WARNING! All changes made in this file will be lost!
*****************************************************************************/

#include "../../../../qt/src/farmapiclient.h"
#include <QtNetwork/QSslError>
#include <QtCore/qmetatype.h>

#include <QtCore/qtmochelpers.h>

#include <memory>


#include <QtCore/qxptype_traits.h>
#if !defined(Q_MOC_OUTPUT_REVISION)
#error "The header file 'farmapiclient.h' doesn't include <QObject>."
#elif Q_MOC_OUTPUT_REVISION != 69
#error "This file was generated using the moc from 6.11.2. It"
#error "cannot be used with the include files from this version of Qt."
#error "(The moc has changed too much.)"
#endif

#ifndef Q_CONSTINIT
#define Q_CONSTINIT
#endif

QT_WARNING_PUSH
QT_WARNING_DISABLE_DEPRECATED
QT_WARNING_DISABLE_GCC("-Wuseless-cast")
namespace {
struct qt_meta_tag_ZN13FarmApiClientE_t {};
} // unnamed namespace

template <> constexpr inline auto FarmApiClient::qt_create_metaobjectdata<qt_meta_tag_ZN13FarmApiClientE_t>()
{
    namespace QMC = QtMocConstants;
    QtMocHelpers::StringRefStorage qt_stringData {
        "FarmApiClient",
        "enteredGame",
        "",
        "loggedOut",
        "loginRequired",
        "message",
        "snapshotUpdated",
        "mailsUpdated",
        "statusMessage",
        "text",
        "errorMessage",
        "connectionChanged",
        "busyChanged",
        "unconfirmedChanged"
    };

    QtMocHelpers::UintData qt_methods {
        // Signal 'enteredGame'
        QtMocHelpers::SignalData<void()>(1, 2, QMC::AccessPublic, QMetaType::Void),
        // Signal 'loggedOut'
        QtMocHelpers::SignalData<void()>(3, 2, QMC::AccessPublic, QMetaType::Void),
        // Signal 'loginRequired'
        QtMocHelpers::SignalData<void(const QString &)>(4, 2, QMC::AccessPublic, QMetaType::Void, {{
            { QMetaType::QString, 5 },
        }}),
        // Signal 'snapshotUpdated'
        QtMocHelpers::SignalData<void()>(6, 2, QMC::AccessPublic, QMetaType::Void),
        // Signal 'mailsUpdated'
        QtMocHelpers::SignalData<void()>(7, 2, QMC::AccessPublic, QMetaType::Void),
        // Signal 'statusMessage'
        QtMocHelpers::SignalData<void(const QString &)>(8, 2, QMC::AccessPublic, QMetaType::Void, {{
            { QMetaType::QString, 9 },
        }}),
        // Signal 'errorMessage'
        QtMocHelpers::SignalData<void(const QString &)>(10, 2, QMC::AccessPublic, QMetaType::Void, {{
            { QMetaType::QString, 9 },
        }}),
        // Signal 'connectionChanged'
        QtMocHelpers::SignalData<void()>(11, 2, QMC::AccessPublic, QMetaType::Void),
        // Signal 'busyChanged'
        QtMocHelpers::SignalData<void()>(12, 2, QMC::AccessPublic, QMetaType::Void),
        // Signal 'unconfirmedChanged'
        QtMocHelpers::SignalData<void()>(13, 2, QMC::AccessPublic, QMetaType::Void),
    };
    QtMocHelpers::UintData qt_properties {
    };
    QtMocHelpers::UintData qt_enums {
    };
    return QtMocHelpers::metaObjectData<FarmApiClient, qt_meta_tag_ZN13FarmApiClientE_t>(QMC::MetaObjectFlag{}, qt_stringData,
            qt_methods, qt_properties, qt_enums);
}
Q_CONSTINIT const QMetaObject FarmApiClient::staticMetaObject = { {
    QMetaObject::SuperData::link<QObject::staticMetaObject>(),
    qt_staticMetaObjectStaticContent<qt_meta_tag_ZN13FarmApiClientE_t>.stringdata,
    qt_staticMetaObjectStaticContent<qt_meta_tag_ZN13FarmApiClientE_t>.data,
    qt_static_metacall,
    nullptr,
    qt_staticMetaObjectRelocatingContent<qt_meta_tag_ZN13FarmApiClientE_t>.metaTypes,
    nullptr
} };

void FarmApiClient::qt_static_metacall(QObject *_o, QMetaObject::Call _c, int _id, void **_a)
{
    auto *_t = static_cast<FarmApiClient *>(_o);
    if (_c == QMetaObject::InvokeMetaMethod) {
        switch (_id) {
        case 0: _t->enteredGame(); break;
        case 1: _t->loggedOut(); break;
        case 2: _t->loginRequired((*reinterpret_cast<std::add_pointer_t<QString>>(_a[1]))); break;
        case 3: _t->snapshotUpdated(); break;
        case 4: _t->mailsUpdated(); break;
        case 5: _t->statusMessage((*reinterpret_cast<std::add_pointer_t<QString>>(_a[1]))); break;
        case 6: _t->errorMessage((*reinterpret_cast<std::add_pointer_t<QString>>(_a[1]))); break;
        case 7: _t->connectionChanged(); break;
        case 8: _t->busyChanged(); break;
        case 9: _t->unconfirmedChanged(); break;
        default: ;
        }
    }
    if (_c == QMetaObject::IndexOfMethod) {
        if (QtMocHelpers::indexOfMethod<void (FarmApiClient::*)()>(_a, &FarmApiClient::enteredGame, 0))
            return;
        if (QtMocHelpers::indexOfMethod<void (FarmApiClient::*)()>(_a, &FarmApiClient::loggedOut, 1))
            return;
        if (QtMocHelpers::indexOfMethod<void (FarmApiClient::*)(const QString & )>(_a, &FarmApiClient::loginRequired, 2))
            return;
        if (QtMocHelpers::indexOfMethod<void (FarmApiClient::*)()>(_a, &FarmApiClient::snapshotUpdated, 3))
            return;
        if (QtMocHelpers::indexOfMethod<void (FarmApiClient::*)()>(_a, &FarmApiClient::mailsUpdated, 4))
            return;
        if (QtMocHelpers::indexOfMethod<void (FarmApiClient::*)(const QString & )>(_a, &FarmApiClient::statusMessage, 5))
            return;
        if (QtMocHelpers::indexOfMethod<void (FarmApiClient::*)(const QString & )>(_a, &FarmApiClient::errorMessage, 6))
            return;
        if (QtMocHelpers::indexOfMethod<void (FarmApiClient::*)()>(_a, &FarmApiClient::connectionChanged, 7))
            return;
        if (QtMocHelpers::indexOfMethod<void (FarmApiClient::*)()>(_a, &FarmApiClient::busyChanged, 8))
            return;
        if (QtMocHelpers::indexOfMethod<void (FarmApiClient::*)()>(_a, &FarmApiClient::unconfirmedChanged, 9))
            return;
    }
}

const QMetaObject *FarmApiClient::metaObject() const
{
    return QObject::d_ptr->metaObject ? QObject::d_ptr->dynamicMetaObject() : &staticMetaObject;
}

void *FarmApiClient::qt_metacast(const char *_clname)
{
    if (!_clname) return nullptr;
    if (!strcmp(_clname, qt_staticMetaObjectStaticContent<qt_meta_tag_ZN13FarmApiClientE_t>.strings))
        return static_cast<void*>(this);
    return QObject::qt_metacast(_clname);
}

int FarmApiClient::qt_metacall(QMetaObject::Call _c, int _id, void **_a)
{
    _id = QObject::qt_metacall(_c, _id, _a);
    if (_id < 0)
        return _id;
    if (_c == QMetaObject::InvokeMetaMethod) {
        if (_id < 10)
            qt_static_metacall(this, _c, _id, _a);
        _id -= 10;
    }
    if (_c == QMetaObject::RegisterMethodArgumentMetaType) {
        if (_id < 10)
            *reinterpret_cast<QMetaType *>(_a[0]) = QMetaType();
        _id -= 10;
    }
    return _id;
}

// SIGNAL 0
void FarmApiClient::enteredGame()
{
    QMetaObject::activate(this, &staticMetaObject, 0, nullptr);
}

// SIGNAL 1
void FarmApiClient::loggedOut()
{
    QMetaObject::activate(this, &staticMetaObject, 1, nullptr);
}

// SIGNAL 2
void FarmApiClient::loginRequired(const QString & _t1)
{
    QMetaObject::activate<void>(this, &staticMetaObject, 2, nullptr, _t1);
}

// SIGNAL 3
void FarmApiClient::snapshotUpdated()
{
    QMetaObject::activate(this, &staticMetaObject, 3, nullptr);
}

// SIGNAL 4
void FarmApiClient::mailsUpdated()
{
    QMetaObject::activate(this, &staticMetaObject, 4, nullptr);
}

// SIGNAL 5
void FarmApiClient::statusMessage(const QString & _t1)
{
    QMetaObject::activate<void>(this, &staticMetaObject, 5, nullptr, _t1);
}

// SIGNAL 6
void FarmApiClient::errorMessage(const QString & _t1)
{
    QMetaObject::activate<void>(this, &staticMetaObject, 6, nullptr, _t1);
}

// SIGNAL 7
void FarmApiClient::connectionChanged()
{
    QMetaObject::activate(this, &staticMetaObject, 7, nullptr);
}

// SIGNAL 8
void FarmApiClient::busyChanged()
{
    QMetaObject::activate(this, &staticMetaObject, 8, nullptr);
}

// SIGNAL 9
void FarmApiClient::unconfirmedChanged()
{
    QMetaObject::activate(this, &staticMetaObject, 9, nullptr);
}
QT_WARNING_POP
